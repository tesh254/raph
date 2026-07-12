package crawler

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
)

const (
	// crawlDialTimeout bounds a single TCP connect (slow-loris / dead host).
	crawlDialTimeout = 10 * time.Second
	// crawlRequestTimeout bounds a whole request incl. body read.
	crawlRequestTimeout = 30 * time.Second
	// crawlMaxBodySize caps a single response so a hostile/huge page can't
	// exhaust memory when it is read fully and converted to markdown.
	crawlMaxBodySize = 10 << 20 // 10 MiB
)

// allowPrivateHosts lets an operator (or the test suite) opt out of the SSRF
// guard to crawl internal documentation. Off by default.
func allowPrivateHosts() bool {
	v := strings.TrimSpace(os.Getenv("RAPH_CRAWL_ALLOW_PRIVATE"))
	return v == "1" || strings.EqualFold(v, "true")
}

// extraBlockedCIDRs covers non-globally-routable ranges that net.IP's helpers
// miss. Go's docs warn IsPrivate "does not describe a security property … and
// should not be used for access control", so we add the ranges that matter for
// SSRF: CGNAT (RFC 6598), benchmarking (RFC 2544), IETF protocol assignments
// (RFC 6890), and deprecated IPv6 site-local (fec0::/10).
var extraBlockedCIDRs = func() []*net.IPNet {
	var nets []*net.IPNet
	for _, c := range []string{
		"100.64.0.0/10", "198.18.0.0/15", "192.0.0.0/24", "fec0::/10",
	} {
		if _, n, err := net.ParseCIDR(c); err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}()

// blockedIP reports whether ip belongs to a range that must never be fetched:
// loopback, RFC1918/ULA private, link-local (which includes the cloud metadata
// endpoint 169.254.169.254), unspecified, multicast, or one of extraBlockedCIDRs.
func blockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	for _, n := range extraBlockedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// safeControl runs after DNS resolution on the *actual* IP being dialed, so it
// guards the seed, every followed redirect, and defeats DNS-rebinding (a name
// that resolves public at check time but internal at connect time).
func safeControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("ssrf guard: bad address %q: %w", address, err)
	}
	if blockedIP(net.ParseIP(host)) {
		return fmt.Errorf("ssrf guard: refusing to connect to non-public address %s", host)
	}
	return nil
}

// safeHTTPTransport builds an http.Transport that refuses connections to
// internal addresses (unless explicitly allowed) with a bounded dial timeout.
func safeHTTPTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   crawlDialTimeout,
		KeepAlive: 30 * time.Second,
	}
	if !allowPrivateHosts() {
		dialer.Control = safeControl
	}
	return &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
