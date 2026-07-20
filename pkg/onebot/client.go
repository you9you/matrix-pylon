package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

type AgentType int

const (
	AgentNapCat AgentType = iota
	AgentLLOneBot
	AgentWeChat
)

type Client struct {
	log zerolog.Logger

	id        string
	token     string
	agentType AgentType
	service   *Service

	eventHandler func(IEvent)

	conn     *websocket.Conn
	connLock sync.Mutex

	isLoggedIn        atomic.Bool
	statusChannel     chan bool
	cancelChecker     context.CancelFunc
	cancelCheckerLock sync.RWMutex

	websocketRequests     map[string]chan<- *Response
	websocketRequestsLock sync.RWMutex
	websocketRequestID    atomic.Int64
}

func NewClient(log zerolog.Logger, id, token string, service *Service) *Client {
	return &Client{
		log:               log.With().Str("client", id).Logger(),
		id:                id,
		token:             token,
		service:           service,
		statusChannel:     make(chan bool),
		websocketRequests: make(map[string]chan<- *Response),
	}
}

func (c *Client) StartLoop(conn *websocket.Conn) {
	c.updateConnection(conn)

	defer func() {
		c.connLock.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.connLock.Unlock()
	}()

	for {
		t, message, err := conn.ReadMessage()
		if err != nil {
			c.log.Warn().Err(err).Msg("Failed to read message from connection")
			return
		}

		if t != websocket.TextMessage {
			continue
		}

		var m map[string]any
		if err := json.Unmarshal(message, &m); err != nil {
			c.log.Warn().Err(err).Msg("Failed to unmarshal JSON")
			break
		}

		c.log.Trace().Msgf("Receive Onebot payload: %+v", m)

		payload, err := UnmarshalPayload(m)
		if err != nil {
			c.log.Warn().Err(err).Msg("Failed to unmarshal payload")
			continue
		}

		switch payload.PayloadType() {
		case PayloadRequest:
			c.log.Warn().Msgf("Unsupported request %s", payload.(*Request).Action)
		case PayloadResponse:
			go c.handleResponse(payload.(*Response))
		case PayloadEvent:
			if c.eventHandler != nil {
				go c.eventHandler(payload.(IEvent))
			}

			switch payload.(IEvent).EventType() {
			case MetaLifecycle:
				lifecycle := payload.(*Lifecycle)
				if lifecycle.SubType == "connect" {
					c.isLoggedIn.Store(true)
				}
			case MetaHeartbeat:
				heartbeat := payload.(*Heartbeat)
				c.cancelCheckerLock.Lock()
				if c.cancelChecker == nil {
					c.startChecker(uint32(heartbeat.Interval))
				}
				c.cancelCheckerLock.Unlock()
				c.statusChannel <- heartbeat.Status.Online
			}
		}
	}
}

func (c *Client) SetEventHandler(handler func(IEvent)) {
	c.eventHandler = handler
}

func (c *Client) Release() {
	c.updateConnection(nil)

	c.cancelCheckerLock.Lock()
	if c.cancelChecker != nil {
		c.cancelChecker()
		c.cancelChecker = nil
	}
	c.cancelCheckerLock.Unlock()

	c.service.removeClient(c.token)
}

func (c *Client) GetToken() string {
	return c.token
}

func (c *Client) IsLoggedIn() bool {
	return c.isLoggedIn.Load()
}

func (c *Client) GetAgentType() AgentType {
	return c.agentType
}

func (c *Client) startChecker(interval uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancelChecker = cancel

	checkInterval := time.Duration(3*interval) * time.Millisecond
	go func() {
		c.log.Info().Msgf("Status checker started, interval: %v", checkInterval)

		for {
			select {
			case status := <-c.statusChannel:
				c.isLoggedIn.Store(status)
			case <-time.After(checkInterval):
				c.isLoggedIn.Store(false)
			case <-ctx.Done():
				c.log.Info().Msgf("Status checker stopped")
				return
			}
		}
	}()
}

func (c *Client) updateConnection(conn *websocket.Conn) {
	c.connLock.Lock()
	defer c.connLock.Unlock()

	if c.conn != nil {
		c.conn.Close()
	}
	c.conn = conn
}

func (c *Client) request(req *Request) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.service.timeout)
	defer cancel()

	req.Echo = fmt.Sprint(c.websocketRequestID.Add(1))

	respChan := make(chan *Response, 1)

	c.addWebsocketResponseWaiter(req.Echo, respChan)
	defer c.removeWebsocketResponseWaiter(req.Echo, respChan)

	c.log.Trace().
		Str("echo", req.Echo).
		Str("action", req.Action).
		Any("timeout", c.service.timeout).
		Msgf("Send Onebot request %+v", req)
	if err := c._request(req); err != nil {
		return nil, err
	}

	select {
	case resp := <-respChan:
		if resp.Status != "ok" {
			return resp, fmt.Errorf("%s Onebot response retcode: %d", resp.Status, resp.Retcode)
		} else {
			return resp.Data, nil
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) _request(req *Request) error {
	c.connLock.Lock()
	defer c.connLock.Unlock()

	if c.conn == nil {
		return errors.New("websocket not connected")
	}

	return c.conn.WriteJSON(req)
}

func (c *Client) handleResponse(resp *Response) {
	c.websocketRequestsLock.RLock()
	respChan, ok := c.websocketRequests[resp.Echo]
	c.websocketRequestsLock.RUnlock()
	if ok {
		select {
		case respChan <- resp:
		default:
			c.log.Warn().Msgf("Failed to handle response to %s: channel didn't accept response", resp.Echo)
		}
	} else {
		c.log.Warn().Msgf("Dropping response to %s: unknown request ID", resp.Echo)
	}
}

func (c *Client) addWebsocketResponseWaiter(echo string, waiter chan<- *Response) {
	c.websocketRequestsLock.Lock()
	c.websocketRequests[echo] = waiter
	c.websocketRequestsLock.Unlock()
}

func (c *Client) removeWebsocketResponseWaiter(echo string, waiter chan<- *Response) {
	c.websocketRequestsLock.Lock()
	existingWaiter, ok := c.websocketRequests[echo]
	if ok && existingWaiter == waiter {
		delete(c.websocketRequests, echo)
	}
	c.websocketRequestsLock.Unlock()
	close(waiter)
}
