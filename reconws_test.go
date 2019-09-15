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

	fmt.Printf("%s\n", b.Duration())
	fmt.Printf("%s\n", b.Duration())
	fmt.Printf("%s\n", b.Duration())
	fmt.Printf("%s\n", b.Duration())
	fmt.Printf("%s\n", b.Duration())
	fmt.Printf("%s\n", b.Duration())
	fmt.Printf("%s\n", b.Duration())
	fmt.Printf("%s\n", b.Duration())

	b.Reset()
	fmt.Printf("%s\n", b.Duration())
	fmt.Printf("%s\n", b.Duration())
	fmt.Printf("%s\n", b.Duration())
	fmt.Printf("%s\n", b.Duration())
	fmt.Printf("%s\n", b.Duration())
	fmt.Printf("%s\n", b.Duration())
	fmt.Printf("%s\n", b.Duration())
	fmt.Printf("%s\n", b.Duration())
	b.Reset()
	lowerBound := []float64{0, 1.0, 2.0, 4.0, 8.0, 16.0, 29.0, 29.0}
	upperBound := []float64{1.0, 2.0, 3.0, 6.0, 10.0, 18.0, 32.0, 32.0}
	last := big.NewFloat(0.0)
	actual := big.NewFloat(0.0)
	for i := 0; i < len(lowerBound); i++ {
		actual.Add(last, big.NewFloat(b.Duration().Seconds()))
		last = actual
		fmt.Printf("Desired timing: %f < %f < %f\n", lowerBound[i], actual, upperBound[i])
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

	start := time.Now()

	lowerBound := []float64{0, 1.0, 2.0, 4.0, 8.0, 16.0, 16.0, 16.0}
	upperBound := []float64{2.0, 4.0, 8.0, 16.0, 35.0, 35.0, 35.0, 35.0}

	iterations := len(lowerBound)

	if testing.Short() {
		fmt.Printf("Reducing length of test in short mode")
		iterations = 3
	}

	for i := 0; i < iterations; i++ {
		<-c
		actual := big.NewFloat(time.Since(start).Seconds())
		fmt.Printf("Desired timing: %f < %f < %f\n", lowerBound[i], actual, upperBound[i])
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
