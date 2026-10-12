package cli

// The client side of the daemon: calls over internal/proto, starting the daemon when it is not
// running (unless told not to).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/bacnx/squad/internal/driver/local"
	"github.com/bacnx/squad/internal/proto"
	"github.com/bacnx/squad/internal/server"
)

// errDown is returned when the daemon is not running and may not be started. Its text is what
// the Paseo plugin looks for.
var errDown = errors.New(proto.DownMessage)

// callError is a refusal from the daemon.
type callError struct {
	Code, Rule, Msg string
}

func (e *callError) Error() string { return e.Msg }

// daemonInfo reads daemon.json.
func daemonInfo() (proto.Info, error) {
	var info proto.Info
	b, err := os.ReadFile(server.DaemonFile(server.Dir()))
	if err != nil {
		return info, errDown
	}
	if err := json.Unmarshal(b, &info); err != nil {
		return info, errDown
	}
	return info, nil
}

// call runs op with token (admin: "" uses the daemon's admin token) and decodes the result into out.
func call(ctx context.Context, op, token string, args, out any) error {
	info, err := daemonInfo()
	if err != nil {
		return err
	}
	if token == "" {
		token = info.Token
	}
	a, err := json.Marshal(args)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(proto.Request{Op: op, Token: token, Args: a})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+info.Addr+"/rpc", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errDown
	}
	defer resp.Body.Close()
	var r proto.Response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("daemon answer: %w", err)
	}
	if !r.OK {
		return &callError{Code: r.Code, Rule: r.Rule, Msg: r.Error}
	}
	if out != nil && len(r.Result) > 0 {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// ensureDaemon makes sure a daemon runs, starting `self serve` detached when none answers.
func ensureDaemon(self string) error {
	err := pingDaemon(2 * time.Second)
	if err == nil {
		return nil
	}
	if !errors.Is(err, errDown) {
		return err
	}
	if err := start(self, local.Detach); err != nil {
		if err := start(self, local.DetachNoBreakaway); err != nil {
			return fmt.Errorf("start daemon: %w", err)
		}
	}
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if pingDaemon(time.Second) == nil {
			return nil
		}
	}
	return fmt.Errorf("the squad daemon did not come up; see %s", server.ServeLog(server.Dir()))
}

func start(self string, detach func(*exec.Cmd)) error {
	cmd := exec.Command(self, "serve")
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
