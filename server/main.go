package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/olahol/melody"
	"github.com/purarue/currently_listening"
	"github.com/urfave/cli/v2"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

type CurrentlyListeningResponse struct {
	Song    *currently_listening.SetListening `json:"song"`
	Playing bool                              `json:"playing"`
}

type WebsocketResponse struct {
	MsgType string      `json:"msg_type"`
	Data    interface{} `json:"data"`
}

func server(port int, password string, staleAfter int64) {
	m := melody.New()
	m.HandleConnect(func(s *melody.Session) {
		log.Printf("Opened connection from %s\n", s.Request.RemoteAddr)
	})

	m.HandleDisconnect(func(s *melody.Session) {
		log.Printf("Closed connection from %s\n", s.Request.RemoteAddr)
	})

	lock := sync.RWMutex{}

	// global state
	var currentlyListeningSong *currently_listening.SetListening
	var currentTimeStamp *int64 // when the song started playing, set by clients
	var lastUpdated int64       // last time song was updated, to prevent stale data from remaining in the global state
	var isCurrentlyPlaying bool

	currentlyListeningJSON := func() ([]byte, error) {
		songBytes, err := json.Marshal(
			WebsocketResponse{
				MsgType: "currently-listening",
				Data: CurrentlyListeningResponse{
					Song:    currentlyListeningSong,
					Playing: isCurrentlyPlaying,
				},
			},
		)
		if err != nil {
			return nil, err
		}
		return songBytes, nil
	}

	go func() {
		for {
			time.Sleep(time.Second * 10)
			lock.Lock()
			if currentlyListeningSong != nil && isCurrentlyPlaying && lastUpdated != 0 {
				if time.Now().Unix()-lastUpdated > staleAfter {
					fmt.Fprintf(os.Stderr, "Been more than %d seconds since last update, clearing currently listening song\n", staleAfter)
					// unset currently playing
					currentlyListeningSong = nil
					currentTimeStamp = nil
					isCurrentlyPlaying = false
					lastUpdated = 0 // reset to 'unset', so we don't clear it again
					// broadcast to all clients
					if cur, err := currentlyListeningJSON(); err == nil {
						err := m.Broadcast(cur)
						if err != nil {
							fmt.Printf("Error broadcasting currently listening song: %s\n", err.Error())
						}
					} else {
						fmt.Println("Error marshalling currently listening song to JSON")
					}
				}
			}
			lock.Unlock()
		}
	}()

	m.HandleMessage(func(s *melody.Session, msg []byte) {
		switch string(msg) {
		case "currently-listening":
			if cur, err := currentlyListeningJSON(); err == nil {
				s.Write(cur)
			} else {
				fmt.Println("Error marshalling currently listening song to JSON")
				s.Write([]byte("Error converting currently listening song to JSON"))
			}
		case "ping":
			jsonBytes, err := json.Marshal(
				WebsocketResponse{
					MsgType: "pong",
					Data:    nil,
				},
			)
			if err != nil {
				log.Fatal(err)
			}
			s.Write(jsonBytes)
		default:
			fmt.Printf("Unknown message: %s\n", string(msg))
		}
	})

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		m.HandleRequest(w, r)
	})

	authdPost := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			w.Write([]byte("only POST requests are allowed"))
			return false
		}

		if r.Header.Get("password") != password {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("invalid password"))
			return false
		}

		return true
	}

	handleError := func(w http.ResponseWriter, err error) {
		fmt.Printf("Error: %s\n", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error converting currently listening song to JSON"))
	}

	http.HandleFunc("/set-listening", func(w http.ResponseWriter, r *http.Request) {
		if !authdPost(w, r) {
			return
		}

		// parse body
		var cur currently_listening.SetListening
		err := json.NewDecoder(r.Body).Decode(&cur)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("error parsing JSON body"))
			return
		}

		// check if currently playing song is newer
		if currentlyListeningSong != nil && currentTimeStamp != nil && cur.StartedAt < *currentTimeStamp {
			msg := fmt.Sprintf("cannot set currently playing song, started before latest known timestamp (started at %d, latest timestamp %d)", cur.StartedAt, *currentTimeStamp)
			fmt.Println(msg)
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(msg))
			return
		}

		// set currently playing
		lock.Lock()
		currentlyListeningSong = &cur
		currentTimeStamp = &cur.StartedAt
		lastUpdated = time.Now().Unix()
		isCurrentlyPlaying = true
		lock.Unlock()

		if sendBody, err := currentlyListeningJSON(); err == nil {
			// broadcast to all clients
			err := m.Broadcast(sendBody)
			if err != nil {
				fmt.Printf("Error broadcasting currently listening song: %s\n", err.Error())
			}
			// respond to POST request
			imgDesc := cur.Base64Image
			if len(imgDesc) > 10 {
				imgDesc = imgDesc[0:10]
			}
			msg := fmt.Sprintf("Set currently playing song to Artist: '%s', Album: '%s', Title: '%s', Image '%s'", cur.Artist, cur.Album, cur.Title, imgDesc)
			fmt.Println(msg)
			w.Write([]byte(msg))
		} else {
			handleError(w, err)
		}
	})

	http.HandleFunc("/clear-listening", func(w http.ResponseWriter, r *http.Request) {
		if !authdPost(w, r) {
			return
		}

		// parse body
		var cur currently_listening.ClearListening
		err := json.NewDecoder(r.Body).Decode(&cur)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("error parsing JSON body"))
			return
		}

		// check if clear-playing request is newer than current timestamp
		if currentTimeStamp != nil && cur.EndedAt < *currentTimeStamp {
			msg := fmt.Sprintf("cannot clear currently playing song, started before latest known timestamp (started at %d, latest timestamp %d)", cur.EndedAt, *currentTimeStamp)
			fmt.Println(msg)
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(msg))
			return
		}

		// unset currently playing
		lock.Lock()
		currentlyListeningSong = nil
		currentTimeStamp = &cur.EndedAt
		lastUpdated = time.Now().Unix()
		isCurrentlyPlaying = false
		lock.Unlock()

		if sendBody, err := currentlyListeningJSON(); err == nil {
			// broadcast to all clients
			err := m.Broadcast(sendBody)
			if err != nil {
				fmt.Printf("Error broadcasting currently listening song: %s\n", err.Error())
			}
			// respond to POST request
			msg := "Unset currently listening song"
			fmt.Println(msg)
			w.Write([]byte(msg))
		} else {
			handleError(w, err)
		}
	})

	// if requesting this from something which might cache this image, add a part of the base64
	// as a secondary path, e.g.
	//
	// e.g. /currently-listening-image/JkFJQ0hJTkdfSU1BR0U9Fg
	http.HandleFunc("/currently-listening-image/", func(w http.ResponseWriter, r *http.Request) {
		if currentlyListeningSong == nil {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("no currently listening song"))
			return
		}

		if currentlyListeningSong.Base64Image == "" {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("no image for currently listening song"))
			return
		}

		// decode base64 image
		decoded, err := base64.StdEncoding.DecodeString(currentlyListeningSong.Base64Image)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("error decoding base64 image"))
			return
		}

		// set content type
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.Itoa(len(decoded)))
		w.Write(decoded)
	})

	fmt.Printf("Listening on port %d\n", port)
	http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
}

func main() {

	app := &cli.App{
		Name:  "currently-listening",
		Usage: "Get the song I'm currently listening to",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "port",
				Value: 3030,
				Usage: "Port to listen on",
			},
			&cli.StringFlag{
				Name:     "password",
				Value:    "",
				Usage:    "Password to authenticate setting the currently listening song",
				Required: true,
				EnvVars:  []string{"CURRENTLY_LISTENING_PASSWORD"},
			},
			&cli.Int64Flag{
				Name:  "stale-after",
				Value: 3600,
				Usage: "Number of seconds after which the currently listening song is considered stale, and will be cleared. Typically, this should be cleared by the client, but this is a fallback to prevent stale state from remaining for long periods of time",
			},
		},
		Action: func(c *cli.Context) error {
			server(c.Int("port"), c.String("password"), c.Int64("stale-after"))
			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
