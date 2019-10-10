/*
   reconws is websocket client that automatically reconnects
   Copyright (C) 2019 Timothy Drysdale <timothy.d.drysdale@gmail.com>

   This program is free software: you can redistribute it and/or modify
   it under the terms of the GNU Affero General Public License as
   published by the Free Software Foundation, either version 3 of the
   License, or (at your option) any later version.

   This program is distributed in the hope that it will be useful,
   but WITHOUT ANY WARRANTY; without even the implied warranty of
   MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
   GNU Affero General Public License for more details.

   You should have received a copy of the GNU Affero General Public License
   along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

package reconws

import (
	"math/rand"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jpillora/backoff"
	log "github.com/sirupsen/logrus"
	"github.com/timdrysdale/chanstats"
)

type WsMessage struct {
	Data []byte
	Type int
}

// connects (retrying/reconnecting if necessary) to websocket server at url

type ReconWs struct {
	Err             error
	ForwardIncoming bool
	In              chan WsMessage
	Out             chan WsMessage
	Retry           RetryConfig
	Stats           *chanstats.ChanStats
	Stop            chan struct{}
	Url             string
	// internal usage only
	close     chan struct{}
	connected chan struct{}
}

type RetryConfig struct {
	Factor  float64
	Jitter  bool
	Min     time.Duration
	Max     time.Duration
	Timeout time.Duration
}

func New() *ReconWs {
	r := &ReconWs{
		Url:             "",
		In:              make(chan WsMessage),
		Out:             make(chan WsMessage),
		close:           make(chan struct{}),
		Stop:            make(chan struct{}),
		ForwardIncoming: true,
		Err:             nil,
		Retry: RetryConfig{Factor: 2,
			Min:     1 * time.Second,
			Max:     10 * time.Second,
			Timeout: 1 * time.Second,
			Jitter:  false},
		Stats: chanstats.New(),
	}
	return r
}

// run this in a separate goroutine so that the connection can be
// ended from where it was initialised, by close((* ReconWs).Stop)
func (r *ReconWs) Reconnect() {

	boff := &backoff.Backoff{
		Min:    r.Retry.Min,
		Max:    r.Retry.Max,
		Factor: r.Retry.Factor,
		Jitter: r.Retry.Jitter,
	}

	rand.Seed(time.Now().UTC().UnixNano())

	// try dialling ....

	for {
		r.connected = make(chan struct{}) //reset our connection indicator
		r.close = make(chan struct{})

		//refresh these indicators each attempt
		stopped := make(chan struct{})

		go func() {
			r.Dial() //returns on error or if closed with close(r.Close)
			log.WithField("error", r.Err).Debug("Reconnect() stopped")
			close(stopped)
		}()

		select {

		case <-r.connected: //good!
			boff.Reset()
			//let the connection operate until an issue arises:
			select {
			case <-stopped: // connection closed, so reconnect
				if r.Err != nil {
					boff.Reset()
				}
			case <-r.Stop: // requested to stop, so disconnect
				log.Debug("(r.Stop)ped after connecting")
				close(r.close) //doClose = true
				return
			}
		case <-r.Stop: //requested to stop
			log.Debug("(r.Stop)ped before connecting")
			close(r.close) //doClose = true
			return
		case <-time.After(r.Retry.Timeout): //too slow, retry
			log.Debug("Timeout before connecting")
			close(r.close)

		}

		nextRetryWait := boff.Duration()

		time.Sleep(nextRetryWait)

	}

}

// Dial the websocket server once.
// If dial fails then return immediately
// If dial succeeds then handle message traffic until
// (r *ReconWs).Close is closed (presumably by the caller)
// or if there is a reading or writing error
func (r *ReconWs) Dial() {

	var err error

	defer func() {
		r.Err = err
	}()

	if r.Url == "" {
		log.Error("Can't dial an empty Url")
		return
	}

	u, err := url.Parse(r.Url)

	if err != nil {
		log.Error("Url:", err)
		return
	}

	if u.Scheme != "ws" && u.Scheme != "wss" {
		log.Error("Url needs to start with ws or wss")
		return
	}

	if u.User != nil {
		log.Error("Url can't contain user name and password")
		return
	}

	// start dialing ....

	log.WithField("To", u).Debug("Connecting")

	c, _, err := websocket.DefaultDialer.Dial(r.Url, nil)

	if err != nil {
		log.WithField("error", err).Error("Dialing")
		return
	}

	defer c.Close()

	r.Stats.ConnectedAt = time.Now()
	close(r.connected) //signal that we've connected

	log.WithField("To", u).Info("Connected")

	// handle our reading tasks

	done := make(chan struct{})

	go func() {
		defer func() {
			close(done) // signal to writing task to exit if we exit first
		}()
		for {

			mt, data, err := c.ReadMessage()

			// Check for errors, e.g. caused by writing task closing conn
			// because we've been instructed to exit
			// log as info since we expect an error here on a normal exit
			if err != nil {
				log.WithField("info", err).Info("Reading")
				break
			}
			// optionally forward messages
			if r.ForwardIncoming {
				r.In <- WsMessage{Data: data, Type: mt}
				//update stats
				r.Stats.Rx.Bytes.Add(float64(len(data)))
				r.Stats.Rx.Dt.Add(time.Since(r.Stats.Rx.Last).Seconds())
				r.Stats.Rx.Last = time.Now()
			}
		}
	}()

	// handle our writing tasks

	for {
		select {
		case <-done:
			return

		case msg := <-r.Out:

			err := c.WriteMessage(msg.Type, msg.Data)
			if err != nil {
				log.WithField("error", err).Error("Writing")
				return
			}
			//update stats
			r.Stats.Tx.Bytes.Add(float64(len(msg.Data)))
			r.Stats.Tx.Dt.Add(time.Since(r.Stats.Tx.Last).Seconds())
			r.Stats.Tx.Last = time.Now()

		case <-r.close: // r.HandshakeTimout has probably expired

			// Cleanly close the connection by sending a close message
			err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.WithField("error", err).Error("Closing")
			} else {
				log.Info("Closed")
			}
			return
		}
	}
}
