package cpa

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReadRESPMessageRejectsOversizedBulkAndArray(t *testing.T) {
	// Bulk length above the cap is rejected without allocating.
	oversized := fmt.Sprintf("$%d\r\n", maxRESPBulkBytes+1)
	_, err := readRESPMessage(bufio.NewReader(strings.NewReader(oversized)))
	if err == nil || !strings.Contains(err.Error(), "RESP bulk too large") {
		t.Fatalf("expected bulk-too-large error, got %v", err)
	}

	// Array count above the cap is rejected without recursing.
	oversizedArray := fmt.Sprintf("*%d\r\n", maxRESPArrayCount+1)
	_, err = readRESPMessage(bufio.NewReader(strings.NewReader(oversizedArray)))
	if err == nil || !strings.Contains(err.Error(), "RESP array too large") {
		t.Fatalf("expected array-too-large error, got %v", err)
	}

	// Boundary values within the cap still parse.
	smallArray := "*1\r\n$2\r\nok\r\n"
	message, err := readRESPMessage(bufio.NewReader(strings.NewReader(smallArray)))
	if err != nil {
		t.Fatalf("unexpected error for small array: %v", err)
	}
	if len(message.array) != 1 || message.array[0].str != "ok" {
		t.Fatalf("unexpected message: %+v", message)
	}
}

func TestReadRESPMessage(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantStr string
		wantErr string
		wantArr []string
	}{
		{
			name:    "simple string",
			input:   "+OK\r\n",
			wantStr: "OK",
		},
		{
			name:    "error",
			input:   "-ERR bad auth\r\n",
			wantErr: "ERR bad auth",
		},
		{
			name:    "bulk string",
			input:   "$5\r\nhello\r\n",
			wantStr: "hello",
		},
		{
			name:    "array of bulk strings",
			input:   "*3\r\n$7\r\nmessage\r\n$5\r\nusage\r\n$2\r\n{}\r\n",
			wantArr: []string{"message", "usage", "{}"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, err := readRESPMessage(bufio.NewReader(strings.NewReader(tt.input)))
			if err != nil {
				t.Fatalf("readRESPMessage() error = %v", err)
			}
			if tt.wantErr != "" {
				if message.err == nil || message.err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", message.err, tt.wantErr)
				}
				return
			}
			if tt.wantArr != nil {
				if len(message.array) != len(tt.wantArr) {
					t.Fatalf("array length = %d, want %d", len(message.array), len(tt.wantArr))
				}
				for i, want := range tt.wantArr {
					if message.array[i].str != want {
						t.Fatalf("array[%d] = %q, want %q", i, message.array[i].str, want)
					}
				}
				return
			}
			if message.str != tt.wantStr {
				t.Fatalf("str = %q, want %q", message.str, tt.wantStr)
			}
		})
	}
}

func TestUsagePubSubPayload(t *testing.T) {
	message := respMessage{array: []respBulkElement{
		{str: "message"},
		{str: "usage"},
		{str: `{"model":"gpt-5.2"}`},
	}}
	payload, ok := usagePubSubPayload(message)
	if !ok {
		t.Fatal("expected payload")
	}
	if string(payload) != `{"model":"gpt-5.2"}` {
		t.Fatalf("payload = %s", payload)
	}
	subscribeFrame := respMessage{array: []respBulkElement{
		{str: "subscribe"},
		{str: "usage"},
		{str: "1"},
	}}
	if _, ok := usagePubSubPayload(subscribeFrame); ok {
		t.Fatal("subscribe frame must not be treated as payload")
	}
}

func TestParseUsageEvent(t *testing.T) {
	payload := []byte(`{
		"timestamp": "2026-08-22T03:00:00Z",
		"auth_index": "abc123",
		"provider": "codex",
		"model": "gpt-5.2",
		"tokens": {"input_tokens": 100, "output_tokens": 50, "reasoning_tokens": 20, "cache_read_tokens": 30},
		"failed": false,
		"response_headers": {"X-Codex-Secondary-Used-Percent": ["3.42"]}
	}`)
	event, err := ParseUsageEvent(payload)
	if err != nil {
		t.Fatalf("ParseUsageEvent() error = %v", err)
	}
	if event.AuthIndex != "abc123" || event.Provider != "codex" || event.Model != "gpt-5.2" {
		t.Fatalf("unexpected identity fields: %+v", event)
	}
	if event.Tokens.InputTokens != 100 || event.Tokens.CacheReadTokens != 30 {
		t.Fatalf("unexpected tokens: %+v", event.Tokens)
	}
	if got := event.HeaderValue("x-codex-secondary-used-percent"); got != "3.42" {
		t.Fatalf("HeaderValue = %q, want 3.42", got)
	}
}

// fakeCPAStream is a minimal CPA-compatible RESP server for stream tests.
type fakeCPAStream struct {
	listener net.Listener
	secret   string
	authed   chan struct{}
	mu       sync.Mutex
	conns    []net.Conn
	closed   bool
}

func newFakeCPAStream(t *testing.T, secret string) *fakeCPAStream {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeCPAStream{
		listener: listener,
		secret:   secret,
		authed:   make(chan struct{}, 1),
	}
	go server.serve()
	return server
}

func (s *fakeCPAStream) addr() string { return s.listener.Addr().String() }

func (s *fakeCPAStream) waitAuth(t *testing.T) {
	t.Helper()
	select {
	case <-s.authed:
	case <-time.After(2 * time.Second):
		t.Fatal("client never authenticated")
	}
}

func (s *fakeCPAStream) publish(t *testing.T, payload string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, conn := range s.conns {
		message := "*3\r\n$7\r\nmessage\r\n$5\r\nusage\r\n$" + itoa(len(payload)) + "\r\n" + payload + "\r\n"
		if _, err := conn.Write([]byte(message)); err != nil {
			t.Logf("publish error: %v", err)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func (s *fakeCPAStream) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	_ = s.listener.Close()
	for _, conn := range s.conns {
		_ = conn.Close()
	}
}

func (s *fakeCPAStream) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		s.conns = append(s.conns, conn)
		s.mu.Unlock()
		go s.handle(conn)
	}
}

func (s *fakeCPAStream) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	authed := false
	for {
		command, err := readRESPMessage(reader)
		if err != nil {
			return
		}
		if len(command.array) == 0 {
			continue
		}
		cmd := strings.ToUpper(command.array[0].str)
		switch cmd {
		case "AUTH":
			if len(command.array) >= 2 && command.array[1].str == s.secret {
				authed = true
				select {
				case s.authed <- struct{}{}:
				default:
				}
				_, _ = writer.WriteString("+OK\r\n")
			} else {
				_, _ = writer.WriteString("-ERR invalid password\r\n")
			}
		case "SUBSCRIBE":
			if !authed {
				_, _ = writer.WriteString("-NOAUTH Authentication required.\r\n")
			} else {
				channel := usageChannelName
				if len(command.array) >= 2 {
					channel = command.array[1].str
				}
				_, _ = writer.WriteString("*3\r\n$9\r\nsubscribe\r\n$" + itoa(len(channel)) + "\r\n" + channel + "\r\n:1\r\n")
			}
		default:
			_, _ = writer.WriteString("-ERR unknown command '" + strings.ToLower(cmd) + "'\r\n")
		}
		if err := writer.Flush(); err != nil {
			return
		}
	}
}

func TestUsageStreamLoopSubscribeAndReceive(t *testing.T) {
	server := newFakeCPAStream(t, "sekrit")
	defer server.close()

	events := make(chan *UsageEvent, 4)
	manager := NewUsageStreamManager(func(_ context.Context, target UsageStreamTarget, event *UsageEvent) {
		events <- event
	})
	defer manager.Close()

	manager.Update([]UsageStreamTarget{{
		InstanceID:       1,
		BaseURL:          "http://" + server.addr(),
		ManagementSecret: "sekrit",
	}})
	server.waitAuth(t)

	server.publish(t, `{"auth_index":"idx1","provider":"codex","model":"gpt-5.2","tokens":{"input_tokens":10,"output_tokens":5}}`)

	select {
	case event := <-events:
		if event.AuthIndex != "idx1" || event.Tokens.InputTokens != 10 {
			t.Fatalf("unexpected event: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for usage event")
	}
}

func TestUsageStreamLoopRejectsBadSecret(t *testing.T) {
	server := newFakeCPAStream(t, "sekrit")
	defer server.close()

	manager := NewUsageStreamManager(func(context.Context, UsageStreamTarget, *UsageEvent) {})
	defer manager.Close()

	manager.Update([]UsageStreamTarget{{
		InstanceID:       2,
		BaseURL:          "http://" + server.addr(),
		ManagementSecret: "wrong",
	}})

	// The loop should stay alive retrying; manager remains functional.
	if len(manager.ActiveTargets()) != 1 {
		t.Fatalf("ActiveTargets = %v, want [2]", manager.ActiveTargets())
	}
}
