package reconws

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestWsEcho(t *testing.T) {

	r := New()

	// Create test server with the echo handler.
	s := httptest.NewServer(http.HandlerFunc(echo))
	defer s.Close()

	// Convert http://127.0.0.1 to ws://127.0.0.
	r.Url = "ws" + strings.TrimPrefix(s.URL, "http")

	go r.Reconnect()

	payload := []byte("Hello")
	mtype := int(websocket.TextMessage)

	r.Out <- WsMessage{Data: payload, Type: mtype}

	reply := <-r.In

	if bytes.Compare(reply.Data, payload) != 0 {
		t.Errorf("Got unexpected response: %s, wanted %s\n", reply.Data, payload)
	}

}

var upgrader = websocket.Upgrader{}

func echo(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()
	for {
		mt, message, err := c.ReadMessage()
		if err != nil {
			break
		}
		err = c.WriteMessage(mt, message)
		if err != nil {
			break
		}
	}
}
