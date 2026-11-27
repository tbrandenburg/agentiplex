package agentiplex

import (
	"fmt"
	"regexp"
	"time"
)

type Config struct {
	BrokerMode string
	BrokerURL string
	BrokerTokenFile string
	BrokerCredentialsFile string
	BrokerCAFile string
	BrokerSync time.Duration // embedded fsync interval; zero fsyncs every write
	OutboundTokenFiles map[string]string
	OutboundCAFile string
	Storage string
	Namespace string
	Replicas int
	AllowedOrigins []string
	Timeout time.Duration
	RetryDelay time.Duration
	Workers int // zero selects default; one restores serial operation
	MaxAttempts int // pre-submission reachability attempts only
	TaskPollInterval time.Duration // zero inherits RetryDelay; no accepted-task lifetime limit
	HealthInterval time.Duration // zero disables periodic observation
	Retention time.Duration // age after which terminal deliveries are removed; zero selects 7d
	UnreturnedRetention time.Duration // longer limit for terminal deliveries no ask handed back; zero selects 30d
}

var validID = regexp.MustCompile("^[a-z0-9][a-z0-9_-]{0,63}$")

func DefaultConfig() Config {
	return Config{BrokerMode: "embedded", Storage: "./data", Namespace: "engineering", Replicas: 1,
		Timeout: 120*time.Second, RetryDelay: 5*time.Second, Workers: 4, MaxAttempts: 10, HealthInterval:30*time.Second}
}

func (c Config) validate() error {
	if !validID.MatchString(c.Namespace) || c.Replicas < 1 || c.Replicas > 5 {
		return fmt.Errorf("invalid namespace or replicas")
	}
	if c.BrokerMode != "embedded" && c.BrokerMode != "external" {
		return fmt.Errorf("broker mode must be embedded or external")
	}
	if c.BrokerMode == "embedded" && (c.Storage == "" || c.Replicas != 1) {
		return fmt.Errorf("embedded broker requires a storage path and one replica")
	}
	if c.BrokerMode == "external" && c.BrokerURL == "" {
		return fmt.Errorf("external broker URL required")
	}
	if c.Timeout < time.Second || c.Timeout > 5*time.Minute || c.RetryDelay < time.Second || c.RetryDelay > time.Minute || c.MaxAttempts < 1 || c.MaxAttempts > 100 {
		return fmt.Errorf("invalid delivery limits")
	}
	if c.BrokerSync!=0&&(c.BrokerSync<time.Second||c.BrokerSync>time.Hour){return fmt.Errorf("invalid broker sync (always or 1s–1h)")}
	if c.Workers<0||c.Workers>16{return fmt.Errorf("invalid delivery worker count (0–16)")}
	if c.TaskPollInterval!=0&&(c.TaskPollInterval<time.Second||c.TaskPollInterval>time.Minute){return fmt.Errorf("invalid task poll interval")}
	if c.HealthInterval!=0&&(c.HealthInterval<time.Second||c.HealthInterval>time.Hour){return fmt.Errorf("invalid health interval")}
	if c.Retention!=0&&(c.Retention<time.Hour||c.Retention>3650*24*time.Hour){return fmt.Errorf("invalid delivery retention (1h–3650d)")}
	if c.UnreturnedRetention!=0&&(c.UnreturnedRetention<time.Hour||c.UnreturnedRetention>3650*24*time.Hour){return fmt.Errorf("invalid unreturned retention (1h–3650d)")}
	r,u:=c.Retention,c.UnreturnedRetention
	if r==0{r=defaultRetention}
	if u==0{u=defaultUnreturnedRetention}
	if u<r{return fmt.Errorf("unreturned retention must not be shorter than retention")}
	return nil
}
