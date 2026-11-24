package agentiplex

import (
	"fmt"
	"regexp"
	"time"
)

type Config struct {
	BrokerMode string
	BrokerURL string
	Storage string
	Namespace string
	Replicas int
	AllowedOrigins []string
	Timeout time.Duration
	RetryDelay time.Duration
	MaxAttempts int
	HealthInterval time.Duration // zero disables periodic observation
}

var validID = regexp.MustCompile("^[a-z0-9][a-z0-9_-]{0,63}$")

func DefaultConfig() Config {
	return Config{BrokerMode: "embedded", Storage: "./data", Namespace: "engineering", Replicas: 1,
		Timeout: 120*time.Second, RetryDelay: 5*time.Second, MaxAttempts: 10, HealthInterval:30*time.Second}
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
	if c.HealthInterval!=0&&(c.HealthInterval<time.Second||c.HealthInterval>time.Hour){return fmt.Errorf("invalid health interval")}
	return nil
}
