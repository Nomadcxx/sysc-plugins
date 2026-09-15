package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Client is the plugin-side protocol helper. It owns stdin/stdout framing so a
// plugin never writes anything except JSON Lines to stdout.
type Client struct {
	enc *Encoder
	dec *Decoder

	mu     sync.Mutex
	hello  *HostHello
	nextID atomic.Uint64
	wait   map[string]chan HostReply
}

// NewClient reads host messages from in and writes plugin messages to out.
func NewClient(in io.Reader, out io.Writer) *Client {
	return &Client{
		enc:  NewEncoder(out),
		dec:  NewDecoder(in, ToPlugin),
		wait: make(map[string]chan HostReply),
	}
}

// Handshake answers host.hello. It must be the first call.
func (c *Client) Handshake(identity Identity) (*HostHello, error) {
	msg, err := c.dec.Decode()
	if err != nil {
		return nil, fmt.Errorf("plugin/v1: handshake: %w", err)
	}
	hello, ok := msg.(*HostHello)
	if !ok {
		return nil, fmt.Errorf("plugin/v1: handshake: first message was %s", TypeOf(msg))
	}
	if err := c.enc.Encode(&PluginHello{
		Protocol:     Version{Major: 1, Minor: 0},
		Plugin:       identity,
		Capabilities: hello.Capabilities,
	}); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.hello = hello
	c.mu.Unlock()
	return hello, nil
}

// Recv reads the next host message. Host replies are delivered to Call
// waiters and not returned.
func (c *Client) Recv() (Message, error) {
	for {
		msg, err := c.dec.Decode()
		if err != nil {
			return nil, err
		}
		if reply, ok := msg.(*HostReply); ok {
			c.mu.Lock()
			ch := c.wait[reply.ID]
			delete(c.wait, reply.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- *reply
			}
			continue
		}
		return msg, nil
	}
}

// Send writes one plugin message.
func (c *Client) Send(m Message) error { return c.enc.Encode(m) }

// Snapshot replaces one view.
func (c *Client) Snapshot(viewID string, revision uint64, root *Node) error {
	return c.Send(&ViewSnapshot{ViewID: viewID, Revision: revision, Root: root})
}

// Patch sends keyed subtree replacements against a base revision.
func (c *Client) Patch(viewID string, base, revision uint64, replacements []Replacement) error {
	return c.Send(&ViewPatch{ViewID: viewID, Base: base, Revision: revision, Replacements: replacements})
}

// Call sends a host.call and waits for the matching reply or ctx.Done.
func (c *Client) Call(ctx context.Context, kind CallKind, params any) (HostReply, error) {
	id := fmt.Sprintf("c%d", c.nextID.Add(1))
	if err := ctx.Err(); err != nil {
		return HostReply{ID: id, Error: err.Error()}, err
	}
	var raw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return HostReply{}, err
		}
		raw = data
	}
	ch := make(chan HostReply, 1)
	c.mu.Lock()
	c.wait[id] = ch
	c.mu.Unlock()
	if err := c.Send(&HostCall{ID: id, Call: kind, Params: raw}); err != nil {
		c.mu.Lock()
		delete(c.wait, id)
		c.mu.Unlock()
		return HostReply{}, err
	}
	select {
	case reply := <-ch:
		return reply, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.wait, id)
		c.mu.Unlock()
		return HostReply{ID: id, Error: ctx.Err().Error()}, ctx.Err()
	}
}
