package host

import (
	"errors"
	"strings"

	"github.com/hazyforge/anvil-agents/internal/onechat"
)

func (c *client) sessionOperation(req request) (any, *protocolError) {
	var body struct {
		SessionID  string              `json:"sessionId"`
		Text       string              `json:"text"`
		RunID      string              `json:"runId"`
		DeliveryID string              `json:"deliveryId"`
		Hook       onechat.SessionHook `json:"hook"`
	}
	if err := decodeBody(req.Body, &body); err != nil {
		return nil, err
	}
	if strings.TrimSpace(body.SessionID) == "" {
		return nil, invalid("sessionId is required")
	}
	switch req.Op {
	case "session.stop":
		if err := c.host.manager.StopSession(body.SessionID); err != nil {
			return nil, sessionError(err)
		}
		return map[string]any{}, nil
	case "session.queue":
		if len(body.Text) > 64<<10 {
			return nil, &protocolError{"too_large", "session message exceeds 64 KiB"}
		}
		if strings.TrimSpace(body.Text) == "" {
			return nil, invalid("session message is required")
		}
		result, err := c.host.manager.QueueSession(c.host.ctx, body.SessionID, body.Text, "")
		if err != nil {
			return nil, sessionError(err)
		}
		return result, nil
	case "session.hook", "session.hook.ack":
		// The socket checks the peer UID; the worker also checks a per-run nonce.
		// Desktop's proxy shares this transport, so the socket alone is not proof
		// that a report came from a harness hook.
		if _, ok := c.conn.(*lineConn); !ok {
			return nil, &protocolError{"unauthorized", "hooks must use the onechat control socket"}
		}
		if req.Op == "session.hook.ack" {
			if err := c.host.manager.AcknowledgeHook(body.SessionID, body.RunID, body.DeliveryID); err != nil {
				return nil, sessionError(err)
			}
			return map[string]any{}, nil
		}
		result, err := c.host.manager.HandleHook(body.SessionID, body.Hook)
		if err != nil {
			return nil, sessionError(err)
		}
		return result, nil
	}
	return nil, invalid("unknown session operation")
}

func sessionError(err error) *protocolError {
	if errors.Is(err, onechat.ErrSessionNotFound) {
		return &protocolError{"not_found", "session is not in the daemon inventory; list sessions and try again"}
	}
	return &protocolError{"internal", err.Error()}
}
