package core

import (
	"time"
)

// Me is what a participant's own `squad mcp` learns about it.
type Me struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Token string `json:"token"`
	Kind  string `json:"kind"`
	Team  string `json:"team"`
	Role  string `json:"role"`
}

func (e *Engine) me(p *Participant) Me {
	return Me{ID: p.ID, Name: p.Name, Token: p.Token, Kind: p.Kind, Team: e.teamName(p.TeamID), Role: p.Role}
}

// RegisterSession registers a session the human opened as a solo, named after its
// directory. Each `squad mcp` a session starts registers once. key is the harness's own session
// id (Claude's CLAUDE_CODE_SESSION_ID): a session that comes back with the same key (resumed,
// its MCP server restarted, the machine rebooted) is the same participant again, with its name,
// team, role and waiting mail. harness is the session's own ("claude"): its gate's workers run
// on it when their role names none.
func (e *Engine) RegisterSession(cwd, model, key, harness string) (Me, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.registerSession(cwd, model, key, harness)
}

// registerSession is RegisterSession for a caller that holds e.mu.
func (e *Engine) registerSession(cwd, model, key, harness string) (Me, error) {
	if key != "" {
		p, err := e.pWhere(`kind=? AND session_ref=?`, KindSession, key)
		if err != nil {
			return Me{}, err
		}
		if p != nil {
			if _, err := e.db.Exec(`UPDATE participants SET status=?, last_active=? WHERE id=?`, StatusLive, now(), p.ID); err != nil {
				return Me{}, err
			}
			return e.me(p), nil
		}
		// A session woken while gone runs as a worker: opening it again takes it back.
		w, err := e.pWhere(`kind=? AND was_session=1 AND session_ref=?`, KindWorker, key)
		if err != nil {
			return Me{}, err
		}
		if w != nil {
			return e.takeBack(w)
		}
	}
	name, err := e.uniqueName(shortDir(cwd), "")
	if err != nil {
		return Me{}, err
	}
	p := &Participant{ID: newID("p_"), Name: name, Kind: KindSession, Status: StatusLive, Cwd: cwd, Model: model,
		SessionRef: key, Token: newToken(), CreatedAt: now(), LastActive: now(), Harness: harness}
	if _, err := e.db.Exec(`INSERT INTO participants(id,name,kind,status,cwd,model,session_ref,token,created_at,last_active,harness) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, p.Kind, p.Status, p.Cwd, p.Model, p.SessionRef, p.Token, p.CreatedAt, p.LastActive, p.Harness); err != nil {
		return Me{}, err
	}
	return e.me(p), nil
}

// Whoami is the caller as its token names it.
func (e *Engine) Whoami(token string) (Me, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.byToken(token)
	if err != nil {
		return Me{}, err
	}
	return e.me(p), nil
}

// Heartbeat keeps a session live; a gone session that beats again (after a daemon restart, or a
// pause) is live again. It returns how many messages wait for the caller.
func (e *Engine) Heartbeat(token string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.byToken(token)
	if err != nil {
		return 0, err
	}
	if p.Kind == KindSession && p.Status == StatusGone {
		_, _ = e.db.Exec(`UPDATE participants SET status=? WHERE id=?`, StatusLive, p.ID)
	}
	e.touch(p.ID)
	return e.unreadFor(p)
}

// unreadFor is what the caller has yet to see: for a worker, mail not handed to its process yet;
// for a hooked session, mail not given to a turn yet.
func (e *Engine) unreadFor(p *Participant) (int, error) {
	if p.Kind != KindWorker && !p.Hooked {
		return e.unread(p.ID)
	}
	var n int
	err := e.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE to_id=? AND acked_at='' AND delivered_at=''`, p.ID).Scan(&n)
	return n, err
}

// Unregister is a session's `squad mcp` ending: the session is gone. It stays in its team (its
// mail waits for it) until the team closes.
func (e *Engine) Unregister(token string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.byToken(token)
	if err != nil {
		return err
	}
	if p.Kind != KindSession {
		return nil
	}
	_, err = e.db.Exec(`UPDATE participants SET status=? WHERE id=?`, StatusGone, p.ID)
	e.closeTaskforcesOf(p.ID, "its caller "+p.Name+" is gone")
	return err
}

// Sweep marks sessions without a recent heartbeat gone and closes taskforces whose caller went
// away. The daemon runs it every few seconds.
func (e *Engine) Sweep() {
	e.mu.Lock()
	defer e.mu.Unlock()
	cut := time.Now().Add(-SessionTimeout).UTC().Format(time.RFC3339)
	gone, _ := e.pList(`kind=? AND status=? AND last_active<?`, KindSession, StatusLive, cut)
	for _, p := range gone {
		_, _ = e.db.Exec(`UPDATE participants SET status=? WHERE id=?`, StatusGone, p.ID)
		e.closeTaskforcesOf(p.ID, "its caller "+p.Name+" is gone")
	}
}

func (e *Engine) unread(id string) (int, error) {
	var n int
	err := e.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE to_id=? AND acked_at=''`, id).Scan(&n)
	return n, err
}
