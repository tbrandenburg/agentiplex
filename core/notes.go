package core

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
// PutState's per-namespace key count (MaxStateKeys) does not count note keys,
// so only the value limit and the bucket MaxBytes can refuse a note.
const (
	MaxNoteTopic=29
	MaxNotesRead=100
	notePrefix="note-"
)

var validTopic=regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
var noteSuffix=regexp.MustCompile(`^[0-9]{20}-[0-9a-f]{8}$`)

// isNoteKey reports whether the full state key k is a note key of namespace.
func isNoteKey(namespace,k string)bool{
	rest,ok:=strings.CutPrefix(k,namespace+"."+notePrefix);if !ok||len(rest)<30{return false}
	return rest[len(rest)-30]=='-'&&noteSuffix.MatchString(rest[len(rest)-29:])
}

type Note struct {
	Key string `json:"key"`
	Topic string `json:"topic"`
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
// The author is the authenticated principal, never a caller-supplied claim.
func (b *Bus) Note(ctx context.Context,namespace,topic,text string)(Note,error){
	author:=authorAs(ctx)
	prefix,err:=noteTopic(namespace,topic);if err!=nil{return Note{},err}
	if strings.TrimSpace(text)==""||!utf8.ValidString(text){return Note{},fmt.Errorf("%w: text must be non-empty UTF-8",ErrInvalid)}
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
	b.emit("state.updated",k,"",author)
	return Note{Key:strings.TrimPrefix(k,namespace+"."),Topic:topic,Author:author,Text:text,At:now},nil
}

// ReadNotes returns the last limit notes of a single topic in key (time) order.
func (b *Bus) ReadNotes(namespace, topic string, limit int) ([]Note, error) {
	if _, err := noteTopic(namespace, topic); err != nil { return nil, err }
	return b.listNotes(namespace, topic, limit)
}

// ListNotes returns the most recent notes across all topics in a namespace,
// in timestamp order. No separate note store or namespace access policy.
func (b *Bus) ListNotes(namespace string, limit int) ([]Note, error) {
	if !validID.MatchString(namespace) { return nil, ErrInvalid }
	return b.listNotes(namespace, "", limit)
}

func (b *Bus) listNotes(namespace, topic string, limit int) ([]Note, error) {
	if limit <= 0 || limit > MaxNotesRead { limit = MaxNotesRead }
	ks, err := keys(b.store.State)
	if err != nil { return nil, err }
	prefix := namespace+"."+notePrefix
	m := []string{}
	for _, k := range ks {
		if !isNoteKey(namespace,k) { continue }
		rest := strings.TrimPrefix(k,prefix)
		t := rest[:len(rest)-30]
		if len(t)>MaxNoteTopic || !validTopic.MatchString(t) || (topic!="" && t!=topic) { continue }
		m=append(m,k)
	}
	// The suffix contains the timestamp and random tie-breaker. Sorting full
	// keys would group by topic rather than time.
	sort.Slice(m,func(i,j int)bool{
		a,b:=m[i][len(m[i])-29:],m[j][len(m[j])-29:]
		if a==b{return m[i]<m[j]}
		return a<b
	})
	if len(m)>limit{m=m[len(m)-limit:]}
	out:=[]Note{}
	for _,k:=range m{
		e,err:=b.store.State.Get(k)
		if errors.Is(stateError(err),ErrNotFound){continue}
		if err!=nil{return nil,err}
		var v noteValue
		if err=json.Unmarshal(e.Value(),&v);err!=nil{continue}
		rest:=strings.TrimPrefix(k,prefix)
		out=append(out,Note{Key:strings.TrimPrefix(k,namespace+"."),Topic:rest[:len(rest)-30],Author:v.Author,Text:v.Text,At:v.At})
	}
	return out,nil
}
