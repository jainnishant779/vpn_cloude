package middleware

import (
	"net"
	"net/http"
	"strings"
)

// RealIP rewrites RemoteAddr from trusted proxy headers only when the direct
// peer is an explicitly trusted proxy address or CIDR. This keeps localhost
// checks and rate limiting safe from spoofed X-Forwarded-For headers.
func RealIP(trustedProxies []string) func(http.Handler) http.Handler {
	trusted := newTrustedProxySet(trustedProxies)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			remoteHost := hostOnly(r.RemoteAddr)
			if !trusted.contains(remoteHost) {
				next.ServeHTTP(w, r)
				return
			}

			if realIP := parseHeaderIP(r.Header.Get("X-Real-IP")); realIP != "" {
				r.RemoteAddr = net.JoinHostPort(realIP, "0")
				next.ServeHTTP(w, r)
				return
			}

			if forwarded := parseForwardedFor(r.Header.Get("X-Forwarded-For")); forwarded != "" {
				r.RemoteAddr = net.JoinHostPort(forwarded, "0")
			}

			next.ServeHTTP(w, r)
		})
	}
}

type trustedProxySet struct {
	exact map[string]struct{}
	cidrs []*net.IPNet
}

func newTrustedProxySet(entries []string) trustedProxySet {
	set := trustedProxySet{exact: make(map[string]struct{})}
	for _, raw := range entries {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if ip := net.ParseIP(value); ip != nil {
			set.exact[ip.String()] = struct{}{}
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err == nil && network != nil {
			set.cidrs = append(set.cidrs, network)
		}
	}
	return set
}

func (s trustedProxySet) contains(raw string) bool {
	host := hostOnly(raw)
	if host == "" {
		return false
	}

	if ip := net.ParseIP(host); ip != nil {
		if _, ok := s.exact[ip.String()]; ok {
			return true
		}
		for _, network := range s.cidrs {
			if network.Contains(ip) {
				return true
			}
		}
	}

	return false
}

func parseForwardedFor(raw string) string {
	for _, part := range strings.Split(raw, ",") {
		if candidate := parseHeaderIP(part); candidate != "" {
			return candidate
		}
	}
	return ""
}

func parseHeaderIP(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if ip := net.ParseIP(value); ip != nil {
		return ip.String()
	}
	return ""
}

func hostOnly(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(value)
	if err == nil {
		return host
	}
	return value
}
