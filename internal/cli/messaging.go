package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/bacnx/squad/internal/proto"
)

// The wake half of the Claude Code session adapter: a hooked session's squad mcp waits for the
// daemon to say its session should wake (idle, with mail), then writes one line into Claude's
// messaging socket (CLAUDE_CODE_MESSAGING_SOCKET, a named pipe on Windows and a unix socket
// elsewhere). Claude takes it as a prompt and starts a turn; the mail itself comes with that turn
// through the hooks.

// dialMessaging connects to Claude's messaging socket (messaging_windows.go, messaging_unix.go).
var dialMessaging = dialMessagingSocket

// wakeLoop runs while the session's squad mcp runs.
func (s *mcpServer) wakeLoop(ctx context.Context) {
	for ctx.Err() == nil {
		s.mu.Lock()
		tok := s.token
		s.mu.Unlock()
		var wake bool
		wctx, cancel := context.WithTimeout(ctx, 70*time.Second)
		err := call(wctx, proto.VerbWakeWait, tok, proto.Args{Wait: 60}, &wake)
		cancel()
		switch {
		case err == nil && wake:
			if err := s.nudge(); err != nil {
				fmt.Fprintf(os.Stderr, "squad mcp: wake: %v\n", err)
			}
		case err == nil, ctx.Err() != nil:
		case errors.Is(err, errDown):
			_ = ensureDaemon(s.self)
			sleepCtx(ctx, time.Second)
		default:
			sleepCtx(ctx, 5*time.Second)
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// nudge writes one prompt into Claude's messaging socket: the auth line when Claude gave a token,
// then the message. #N differs every time, since Claude drops identical repeats.
func (s *mcpServer) nudge() error {
	sock := s.getenv("CLAUDE_CODE_MESSAGING_SOCKET")
	if sock == "" {
		return errors.New("no CLAUDE_CODE_MESSAGING_SOCKET")
	}
	s.mu.Lock()
	s.nudges++
	n := s.nudges
	s.mu.Unlock()
	var lines []byte
	if tk := s.getenv("CLAUDE_CODE_MESSAGING_TOKEN"); tk != "" {
		auth, _ := json.Marshal(map[string]string{"type": "auth", "token": tk})
		lines = append(append(lines, auth...), '\n')
	}
	msg, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{
		"content": fmt.Sprintf("[squad] wake #%d: you have new mail; it is shown with this turn.", n)}})
	lines = append(append(lines, msg...), '\n')
	c, err := dialMessaging(sock)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Write(lines)
	return err
}
