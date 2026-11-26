package agentiplex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nats-io/nats.go"
)

// A note is one create-only state key "note-<topic>-<20-digit unix nanos>-<8 hex>",
// so the key part stays a valid state ID (64 chars max) and sorts by time.
// Notes bypass PutState's per-namespace key count (MaxStateKeys); only the
// value limit and the bucket MaxBytes can refuse a note.
const (
	MaxNoteTopic=29
	MaxNotesRead=100
	notePrefix="note-"
)

var validTopic=regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
var noteSuffix=regexp.MustCompile(`^[0-9]{20}-[0-9a-f]{8}$`)

type Note struct {
	Key string `json:"key"`
	Author string `json:"author,omitempty"`
	Text string `json:"text"`
	At time.Time `json:"at"`
}

type noteValue struct {
	Author string `json:"author,omitempty"`
	Text string `json:"text"`
	At time.Time `json:"at"`
}

func noteTopic(namespace,topic string)(string,error){
	if !validID.MatchString(namespace)||len(topic)>MaxNoteTopic||!validTopic.MatchString(topic){return "",fmt.Errorf("%w: topic must match [a-z0-9][a-z0-9_-]* with at most %d characters",ErrInvalid,MaxNoteTopic)}
	return namespace+"."+notePrefix+topic+"-",nil
}

// Note appends one note. Concurrent writers never conflict and nothing is overwritten.
func (b *Bus) Note(ctx context.Context,namespace,topic,text,author string)(Note,error){
	prefix,err:=noteTopic(namespace,topic);if err!=nil{return Note{},err}
	if strings.TrimSpace(text)==""||!utf8.ValidString(text)||!utf8.ValidString(author)||len(author)>128{return Note{},fmt.Errorf("%w: text must be non-empty UTF-8 and author at most 128 bytes",ErrInvalid)}
	now:=time.Now().UTC()
	val,err:=json.Marshal(noteValue{Author:author,Text:text,At:now});if err!=nil{return Note{},err}
	if len(val)>MaxStateValueBytes{return Note{},fmt.Errorf("%w: encoded note exceeds the %d byte value limit",ErrInvalid,MaxStateValueBytes)}
	r:=make([]byte,4);if _,err=rand.Read(r);err!=nil{return Note{},err}
	k:=prefix+fmt.Sprintf("%020d-%s",now.UnixNano(),hex.EncodeToString(r))
	b.mu.Lock();defer b.mu.Unlock()
	if ctx.Err()!=nil{return Note{},ctx.Err()}
	if _,err=b.store.State.Create(k,val);err!=nil{
		if errors.Is(err,nats.ErrKeyExists){return Note{},ErrConflict}
		return Note{},fmt.Errorf("note refused (value limit %d bytes or bucket MaxBytes reached): %w",MaxStateValueBytes,err)
	}
	b.emit("state.updated",k,"")
	return Note{Key:strings.TrimPrefix(k,namespace+"."),Author:author,Text:text,At:now},nil
}

// ReadNotes returns the last limit notes of a topic in key (time) order.
func (b *Bus) ReadNotes(namespace,topic string,limit int)([]Note,error){
	prefix,err:=noteTopic(namespace,topic);if err!=nil{return nil,err}
	if limit<=0||limit>MaxNotesRead{limit=MaxNotesRead}
	ks,err:=keys(b.store.State);if err!=nil{return nil,err}
	m:=[]string{}
	for _,k:=range ks{if s,ok:=strings.CutPrefix(k,prefix);ok&&noteSuffix.MatchString(s){m=append(m,k)}}
	sort.Strings(m)
	if len(m)>limit{m=m[len(m)-limit:]}
	out:=[]Note{}
	for _,k:=range m{
		e,err:=b.store.State.Get(k)
		if errors.Is(stateError(err),ErrNotFound){continue}
		if err!=nil{return nil,err}
		var v noteValue
		if err=json.Unmarshal(e.Value(),&v);err!=nil{continue}
		out=append(out,Note{Key:strings.TrimPrefix(k,namespace+"."),Author:v.Author,Text:v.Text,At:v.At})
	}
	return out,nil
}
