package security

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// ParseHTTPURL validates a recipient URL before origin comparisons.
func ParseHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || u.Opaque != "" {
		return nil, errors.New("expected HTTP(S) URL without credentials, query or fragment")
	}
	// Brackets are reserved for IPv6 literals. Do not turn malformed
	// bracketed DNS hosts, unbracketed IPv6 or zone identifiers into
	// credentials for a different canonical host.
	host := u.Hostname()
	if strings.HasPrefix(u.Host, "[") || strings.Contains(host, ":") {
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.Is6() || ip.Zone() != "" || !strings.HasPrefix(u.Host, "[") {
			return nil, errors.New("invalid or scoped IPv6 origin")
		}
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil, errors.New("invalid HTTP(S) port")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("invalid HTTP(S) port")
		}
	}
	return u, nil
}

// HTTPOrigin canonicalizes the scheme, host and effective numeric port.
// It does not resolve DNS names or equate different DNS aliases.
func HTTPOrigin(u *url.URL) string {
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		host = ip.String()
	} else {
		host = strings.ToLower(host)
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	} else if n, err := strconv.Atoi(port); err == nil {
		port = strconv.Itoa(n)
	}
	return u.Scheme + "://" + net.JoinHostPort(host, port)
}

// HTTPSCredentialOrigin accepts only origin keys, never endpoint paths.
func HTTPSCredentialOrigin(raw string) (string, error) {
	u, err := ParseHTTPURL(raw)
	if err != nil || u.Scheme != "https" || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return "", errors.New("outbound credentials require an HTTPS origin without a path")
	}
	return HTTPOrigin(u), nil
}

// MergeOutboundTokenFile accepts an additional declaration only when its
// credential is readable and agrees with any credential for this origin.
// Errors never contain credential contents or file paths.
func MergeOutboundTokenFile(out map[string]string, origin, file string) error {
	token, err := ReadToken(file)
	if err != nil {
		return err
	}
	if prev, exists := out[origin]; exists {
		previous, err := ReadToken(prev)
		if err != nil {
			return err
		}
		if previous != token {
			return fmt.Errorf("conflicting outbound credential for origin %s", origin)
		}
		return nil
	}
	out[origin] = file
	return nil
}

// NormalizeOutboundTokenFiles validates every explicit origin before merging
// aliases in sorted order. No partial result is returned on failure.
func NormalizeOutboundTokenFiles(existing map[string]string) (map[string]string, error) {
	out := map[string]string{}
	keys := make([]string, 0, len(existing))
	for raw := range existing {
		keys = append(keys, raw)
	}
	sort.Strings(keys)
	for _, raw := range keys {
		origin, err := HTTPSCredentialOrigin(raw)
		if err != nil {
			return nil, err
		}
		if err := MergeOutboundTokenFile(out, origin, existing[raw]); err != nil {
			return nil, err
		}
	}
	return out, nil
}
