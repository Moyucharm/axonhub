package cpa

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/looplj/axonhub/internal/log"
)

const (
	usageChannelName        = "usage"
	dialTimeout             = 10 * time.Second
	initialReconnectBackoff = time.Second
	maxReconnectBackoff     = 60 * time.Second
	maxRESPBulkBytes        = 4 << 20 // 4 MiB upper bound for a single RESP bulk frame.
	maxRESPArrayCount       = 1024
)

// UsageEventTokens mirrors the token stats block of CPA's usage payload.
type UsageEventTokens struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	ReasoningTokens     int64 `json:"reasoning_tokens"`
	CachedTokens        int64 `json:"cached_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
}

// UsageEvent mirrors CLIProxyAPI's queued usage detail payload (the records
// published on the management Redis-compatible "usage" channel).
type UsageEvent struct {
	Timestamp       time.Time        `json:"timestamp"`
	AuthIndex       string           `json:"auth_index"`
	Provider        string           `json:"provider"`
	ExecutorType    string           `json:"executor_type"`
	Model           string           `json:"model"`
	Alias           string           `json:"alias"`
	AuthType        string           `json:"auth_type"`
	APIKey          string           `json:"api_key"`
	Source          string           `json:"source"`
	Failed          bool             `json:"failed"`
	Tokens          UsageEventTokens `json:"tokens"`
	ResponseHeaders map[string]any   `json:"response_headers,omitempty"`
}

// HeaderValue returns the first value of the named response header
// (case-insensitive), or an empty string when absent.
func (e *UsageEvent) HeaderValue(name string) string {
	if len(e.ResponseHeaders) == 0 {
		return ""
	}
	for key, value := range e.ResponseHeaders {
		if !strings.EqualFold(strings.TrimSpace(key), name) {
			continue
		}
		return firstHeaderString(value)
	}
	return ""
}

func firstHeaderString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
		return ""
	default:
		return ""
	}
}

// ParseUsageEvent decodes one CPA usage channel payload.
func ParseUsageEvent(payload []byte) (*UsageEvent, error) {
	var event UsageEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, fmt.Errorf("decode CPA usage event: %w", err)
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	return &event, nil
}

// UsageStreamTarget describes one CPA instance to subscribe to.
type UsageStreamTarget struct {
	InstanceID       int
	BaseURL          string
	ManagementSecret string
	InsecureSkipTLS  bool
}

// UsageStreamHandler receives every decoded usage event from a target.
type UsageStreamHandler func(ctx context.Context, target UsageStreamTarget, event *UsageEvent)

// UsageStreamManager keeps one RESP subscription loop per enabled instance and
// reconciles connections whenever the instance set changes.
type UsageStreamManager struct {
	mu      sync.Mutex
	targets map[int]*usageStreamLoop
	handler UsageStreamHandler
	closed  bool
}

// NewUsageStreamManager creates a manager dispatching events to handler.
func NewUsageStreamManager(handler UsageStreamHandler) *UsageStreamManager {
	return &UsageStreamManager{
		targets: make(map[int]*usageStreamLoop),
		handler: handler,
	}
}

// Update reconciles running subscription loops with the given target list.
func (m *UsageStreamManager) Update(targets []UsageStreamTarget) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	desired := make(map[int]UsageStreamTarget, len(targets))
	for _, target := range targets {
		if target.InstanceID <= 0 || strings.TrimSpace(target.BaseURL) == "" || strings.TrimSpace(target.ManagementSecret) == "" {
			continue
		}
		desired[target.InstanceID] = target
	}
	for id, loop := range m.targets {
		want, ok := desired[id]
		if ok && loop.sameConfig(want) {
			delete(desired, id)
			continue
		}
		loop.stop()
		delete(m.targets, id)
	}
	for _, want := range desired {
		loop := newUsageStreamLoop(want, m.handler)
		m.targets[want.InstanceID] = loop
		go loop.run()
	}
	m.mu.Unlock()
}

// Close stops every subscription loop.
func (m *UsageStreamManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	for _, loop := range m.targets {
		loop.stop()
	}
	m.targets = make(map[int]*usageStreamLoop)
}

// ActiveTargets reports the currently subscribed instance IDs (test helper).
func (m *UsageStreamManager) ActiveTargets() []int {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]int, 0, len(m.targets))
	for id := range m.targets {
		ids = append(ids, id)
	}
	return ids
}

type usageStreamLoop struct {
	target  UsageStreamTarget
	handler UsageStreamHandler
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
}

func newUsageStreamLoop(target UsageStreamTarget, handler UsageStreamHandler) *usageStreamLoop {
	ctx, cancel := context.WithCancel(context.Background())
	return &usageStreamLoop{
		target:  target,
		handler: handler,
		ctx:     ctx,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
}

func (l *usageStreamLoop) sameConfig(other UsageStreamTarget) bool {
	return l.target.BaseURL == other.BaseURL &&
		l.target.ManagementSecret == other.ManagementSecret &&
		l.target.InsecureSkipTLS == other.InsecureSkipTLS
}

func (l *usageStreamLoop) stop() {
	l.cancel()
	select {
	case <-l.done:
	case <-time.After(3 * time.Second):
	}
}

func (l *usageStreamLoop) run() {
	defer close(l.done)
	backoff := initialReconnectBackoff
	for {
		if l.ctx.Err() != nil {
			return
		}
		err := l.subscribeOnce(l.ctx)
		if l.ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Warn(l.ctx, "CPA usage stream attempt failed",
				log.Int("instance_id", l.target.InstanceID),
				log.String("base_url", l.target.BaseURL),
				log.Cause(err),
			)
		}
		select {
		case <-l.ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxReconnectBackoff {
			backoff = maxReconnectBackoff
		}
	}
}

// subscribeOnce dials, authenticates, subscribes and streams until error or ctx done.
func (l *usageStreamLoop) subscribeOnce(ctx context.Context) error {
	subscribedLogged := false
	conn, err := dialUsageStream(l.target)
	if err != nil {
		return err
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	if err := writeRESPCommand(writer, "AUTH", l.target.ManagementSecret); err != nil {
		return err
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	reply, err := readRESPMessage(reader)
	if err != nil {
		return err
	}
	if reply.err != nil {
		return fmt.Errorf("CPA usage stream AUTH failed: %s", reply.err.Error())
	}

	if err := writeRESPCommand(writer, "SUBSCRIBE", usageChannelName); err != nil {
		return err
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	log.Info(ctx, "CPA usage stream subscribing",
		log.Int("instance_id", l.target.InstanceID),
		log.String("channel", usageChannelName),
	)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		message, errRead := readRESPMessage(reader)
		if errRead != nil {
			return errRead
		}
		if message.err != nil {
			return fmt.Errorf("CPA usage stream error: %s", message.err.Error())
		}
		payload, ok := usagePubSubPayload(message)
		if !ok {
			// Subscribe/unsubscribe/pong control frames; log the acknowledgement
			// once so a working handshake is visible.
			if !subscribedLogged && isSubscribeConfirmation(message) {
				subscribedLogged = true
				log.Info(ctx, "CPA usage stream subscribed",
					log.Int("instance_id", l.target.InstanceID),
					log.String("channel", usageChannelName),
				)
			}
			continue
		}
		event, errDecode := ParseUsageEvent(payload)
		if errDecode != nil {
			continue // malformed payloads must not kill the stream
		}
		l.handler(ctx, l.target, event)
	}
}

// dialUsageStream opens the raw TCP/TLS connection to the CPA main port.
func dialUsageStream(target UsageStreamTarget) (net.Conn, error) {
	parsed, err := url.Parse(NormalizedHostURL(target.BaseURL))
	if err != nil {
		return nil, fmt.Errorf("parse CPA base URL for usage stream: %w", err)
	}
	host := parsed.Host
	if host == "" {
		return nil, fmt.Errorf("CPA base URL host is required")
	}
	if parsed.Scheme == "https" {
		return tls.DialWithDialer(
			&net.Dialer{Timeout: dialTimeout},
			"tcp",
			host,
			&tls.Config{
				MinVersion:         tls.VersionTLS12,
				ServerName:         hostnameOf(host),
				InsecureSkipVerify: target.InsecureSkipTLS, //nolint:gosec // Explicit per-instance user setting with UI warning.
			},
		)
	}
	return net.DialTimeout("tcp", host, dialTimeout)
}

// NormalizedHostURL reuses NormalizeBaseURL then strips any path so only
// scheme://host[:port] remains for the raw socket dial.
func NormalizedHostURL(raw string) string {
	normalized, err := NormalizeBaseURL(raw)
	if err != nil {
		return raw
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return normalized
	}
	parsed.Path = ""
	parsed.RawPath = ""
	return parsed.String()
}

func hostnameOf(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport
	}
	return host
}

func usagePubSubPayload(message respMessage) ([]byte, bool) {
	if len(message.array) != 3 {
		return nil, false
	}
	kind := strings.ToLower(message.array[0].str)
	channel := strings.ToLower(message.array[1].str)
	if kind != "message" || channel != usageChannelName {
		return nil, false
	}
	return []byte(message.array[2].str), true
}

// isSubscribeConfirmation reports whether the reply is the expected
// ["subscribe", <channel>, <count>] acknowledgement frame.
func isSubscribeConfirmation(message respMessage) bool {
	return len(message.array) == 3 && strings.ToLower(message.array[0].str) == "subscribe"
}

func writeRESPCommand(writer *bufio.Writer, args ...string) error {
	if _, err := fmt.Fprintf(writer, "*%d\r\n", len(args)); err != nil {
		return err
	}
	for _, arg := range args {
		if _, err := fmt.Fprintf(writer, "$%d\r\n%s\r\n", len(arg), arg); err != nil {
			return err
		}
	}
	return nil
}

type respMessage struct {
	str   string            // bulk/simple string or error text
	err   error             // server error reply (-ERR)
	array []respBulkElement // array elements
}

type respBulkElement struct {
	str string
}

// readRESPMessage reads exactly one RESP reply. It supports simple strings,
// errors, integers, bulk strings and arrays of bulk strings — the subset used
// by CPA's management Redis-compatible protocol.
func readRESPMessage(reader *bufio.Reader) (respMessage, error) {
	prefix, err := reader.ReadByte()
	if err != nil {
		return respMessage{}, err
	}
	line, errLine := readRESPLine(reader)
	if errLine != nil {
		return respMessage{}, errLine
	}
	switch prefix {
	case '+':
		return respMessage{str: line}, nil
	case '-':
		return respMessage{err: fmt.Errorf("%s", line)}, nil
	case ':':
		return respMessage{str: line}, nil
	case '$':
		length, errParse := strconv.Atoi(line)
		if errParse != nil {
			return respMessage{}, fmt.Errorf("RESP protocol error: bad bulk length %q", line)
		}
		if length < 0 {
			return respMessage{}, nil // null bulk string
		}
		if length > maxRESPBulkBytes {
			return respMessage{}, fmt.Errorf("RESP bulk too large: %d", length)
		}
		buf := make([]byte, length+2)
		if _, errRead := readFull(reader, buf); errRead != nil {
			return respMessage{}, errRead
		}
		if buf[length] != '\r' || buf[length+1] != '\n' {
			return respMessage{}, fmt.Errorf("RESP protocol error: bad bulk terminator")
		}
		return respMessage{str: string(buf[:length])}, nil
	case '*':
		count, errParse := strconv.Atoi(line)
		if errParse != nil {
			return respMessage{}, fmt.Errorf("RESP protocol error: bad array length %q", line)
		}
		if count < 0 {
			return respMessage{}, nil // null array
		}
		if count > maxRESPArrayCount {
			return respMessage{}, fmt.Errorf("RESP array too large: %d", count)
		}
		message := respMessage{}
		for i := 0; i < count; i++ {
			element, errElement := readRESPMessage(reader)
			if errElement != nil {
				return respMessage{}, errElement
			}
			message.array = append(message.array, respBulkElement{str: element.str})
		}
		return message, nil
	default:
		return respMessage{}, fmt.Errorf("RESP protocol error: unexpected prefix %q", string(prefix))
	}
}

func readRESPLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

func readFull(reader *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := reader.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
