package reconws

import (
	"bytes"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jpillora/backoff"
)

func TestBackoff(t *testing.T) {

	b := &backoff.Backoff{
		Min:    time.Second,
		Max:    30 * time.Second,
		Factor: 2,
		Jitter: false,
	}

	lowerBound := []float64{0.5, 1.5, 3.5, 7.5, 15.5, 29.5, 29.5, 29.5}
	upperBound := []float64{1.5, 2.5, 4.5, 8.5, 16.5, 30.5, 30.5, 30.5}

	for i := 0; i < len(lowerBound); i++ {

		actual := big.NewFloat(b.Duration().Seconds())

		if actual.Cmp(big.NewFloat(upperBound[i])) > 0 {
			t.Errorf("retry timing was incorrect, iteration %d, elapsed %f, wanted <%f\n", i, actual, upperBound[i])
		}
		if actual.Cmp(big.NewFloat(lowerBound[i])) < 0 {
			t.Errorf("retry timing was incorrect, iteration %d, elapsed %f, wanted >%f\n", i, actual, lowerBound[i])
		}

	}

}

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

func TestRetryTiming(t *testing.T) {

	r := New()

	c := make(chan int)

	// Create test server with the echo handler.
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deny(w, r, c)
	}))
	defer s.Close()

	// Convert http://127.0.0.1 to ws://127.0.0.
	r.Url = "ws" + strings.TrimPrefix(s.URL, "http")

	go r.Reconnect()

	// first failed connection should be immediate
	// backoff with jitter means we quite can't be sure what the timings are
	lowerBound := []float64{0.0, 2.0, 2.0, 2.0, 2.0, 2.0, 2.0, 2.0}
	upperBound := []float64{1.5, 11.5, 11.5, 11.5, 11.5, 11.5, 11.5, 11.5}

	iterations := len(lowerBound)

	if testing.Short() {
		fmt.Printf("Reducing length of test in short mode")
		iterations = 3
	}

	for i := 0; i < iterations; i++ {

		start := time.Now()

		<-c // wait for deny handler to return a value (note: bad handshake due to use of deny handler)

		actual := big.NewFloat(time.Since(start).Seconds())

		if actual.Cmp(big.NewFloat(upperBound[i])) > 0 {
			t.Errorf("retry timing was incorrect, iteration %d, elapsed %f, wanted <%f\n", i, actual, upperBound[i])
		}
		if actual.Cmp(big.NewFloat(lowerBound[i])) < 0 {
			t.Errorf("retry timing was incorrect, iteration %d, elapsed %f, wanted >%f\n", i, actual, lowerBound[i])
		}

	}

	close(r.Stop)

}

func TestReconnectAfterDisconnect(t *testing.T) {

	r := New()

	c := make(chan int)

	n := 0

	// Create test server with the echo handler.
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connectAfterTrying(w, r, &n, 2, c)
	}))
	defer s.Close()

	// Convert http://127.0.0.1 to ws://127.0.0.
	r.Url = "ws" + strings.TrimPrefix(s.URL, "http")

	go r.Reconnect()

	// first failed connection should be immediate
	// should connect on third try
	// then next attempt after that should fail immediately
	// backoff with jitter means we quite can't be sure what the timings are
	// fail immediately, wait retry and fail, wait retry and connect, fail immediately, wait retry and fail
	lowerBound := []float64{0.0, 1.5, 2.0, 0.0, 2.0}
	upperBound := []float64{1.5, 11.5, 11.5, 1.5, 11.5}

	iterations := len(lowerBound)

	if testing.Short() {
		fmt.Printf("Reducing length of test in short mode")
		iterations = 5
	}

	for i := 0; i < iterations; i++ {

		start := time.Now()

		<-c // wait for deny handler to return a value (note: bad handshake due to use of deny handler)

		actual := big.NewFloat(time.Since(start).Seconds())

		if actual.Cmp(big.NewFloat(upperBound[i])) > 0 {
			t.Errorf("retry timing was incorrect, iteration %d, elapsed %f, wanted <%f\n", i, actual, upperBound[i])
		}
		if actual.Cmp(big.NewFloat(lowerBound[i])) < 0 {
			t.Errorf("retry timing was incorrect, iteration %d, elapsed %f, wanted >%f\n", i, actual, lowerBound[i])
		}

	}

	close(r.Stop)

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

func deny(w http.ResponseWriter, r *http.Request, c chan int) {
	c <- 0
}

func connectAfterTrying(w http.ResponseWriter, r *http.Request, n *int, connectAt int, c chan int) {

	defer func() { *n += 1 }()

	c <- *n

	if *n == connectAt {

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		defer conn.Close()

		// immediately close
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))

	}
}
