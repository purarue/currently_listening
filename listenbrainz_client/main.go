package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/purarue/currently_listening"
	"github.com/urfave/cli/v2"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"
)

type ListenBrainzListen struct {
	TrackMetadata struct {
		Artist_name  string `json:"artist_name"`
		Track_name   string `json:"track_name"`
		Release_name string `json:"release_name"`
	} `json:"track_metadata"`
}

func ListenChanged(c *currently_listening.SetListening, l *ListenBrainzListen) bool {
	return c.Artist != l.TrackMetadata.Artist_name && c.Album != l.TrackMetadata.Release_name && c.Title != l.TrackMetadata.Track_name
}

type ListenBrainzPayload struct {
	Playing_now bool                 `json:"playing_now"`
	Count       int                  `json:"count"`
	Listens     []ListenBrainzListen `json:"listens"`
}

type ListenBrainzResponse struct {
	Payload ListenBrainzPayload `json:"payload"`
}

func (p *ListenBrainzResponse) NoSongPlaying() bool {
	return len(p.Payload.Listens) == 0
}

func (p *ListenBrainzResponse) CurrentlyPlaying() *ListenBrainzListen {
	if p.Payload.Count == 0 {
		return nil
	}
	if len(p.Payload.Listens) > 0 && p.Payload.Playing_now {
		return &p.Payload.Listens[0]
	}
	return nil
}

func pollListenbrainz(username string, password string, serverUrl string, debug bool, pollInterval int, pollIntervalWhenListening int) {
	url := fmt.Sprintf("https://api.listenbrainz.org/1/user/%s/playing-now", username)
	var currentlyPlaying *currently_listening.SetListening

	debugPrint := func(msg string) {
		if debug {
			log.Printf("DEBUG: %s\n", msg)
		}
	}

	serverRequest := func(body interface{}, path string) error {
		client := &http.Client{}
		var bodyBytes []byte
		if body == nil {
			bodyBytes = []byte("{}")
		} else {
			marshalledBytes, err := json.Marshal(body)
			if err != nil {
				return err
			}
			bodyBytes = marshalledBytes
		}
		req, err := http.NewRequest("POST", fmt.Sprintf("%s/%s", serverUrl, path), ioutil.NopCloser(bytes.NewReader(bodyBytes)))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("password", password)
		resp, err := client.Do(req)
		if err != nil {
			log.Fatalf("Error sending %s to server: %s\n", path, err.Error())
		}
		if resp.StatusCode != 200 {
			fmt.Fprintf(os.Stderr, "Error sending %s to server: %s\n", path, resp.Status)
		}
		defer resp.Body.Close()
		serverResp, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Fatalf("Error reading response from server: %s", err.Error())
		}
		debugPrint(fmt.Sprintf("Response from server: %s", string(serverResp)))
		return nil
	}

	sleep := func() {
		if currentlyPlaying != nil {
			time.Sleep(time.Duration(pollIntervalWhenListening) * time.Second)
		} else {
			time.Sleep(time.Duration(pollInterval) * time.Second)
		}
	}

	for {
		lresp, err := http.Get(url)
		if err != nil {
			fmt.Printf("Error fetching from ListenBrainz: %s\n", err.Error())
			sleep()
			continue
		}

		if lresp.StatusCode != 200 {
			fmt.Printf("Error %s fetching from ListenBrainz: %s\n", lresp.Status, lresp.Status)
			sleep()
			continue
		}

		var listenbrainzResponse *ListenBrainzResponse
		err = json.NewDecoder(lresp.Body).Decode(&listenbrainzResponse)
		if err != nil {
			log.Fatalf("Error converting ListenBrainz to struct: %s\n", err.Error())
		}
		debugPrint(fmt.Sprintf("Response from ListenBrainz: %+v", listenbrainzResponse))
		lresp.Body.Close()

		if listenbrainzResponse == nil {
			log.Fatalf("No ListenBrainz response received %+v %+v", listenbrainzResponse, lresp)
		}

		// if no song is currently playing and we have a currently playing song, send a request to the server to clear it
		if listenbrainzResponse.NoSongPlaying() && currentlyPlaying != nil {
			fmt.Println("No song currently playing, clearing currently playing song")
			err = serverRequest(currently_listening.ClearListening{
				EndedAt: time.Now().Unix(),
			}, "clear-listening")
			if err != nil {
				log.Fatalf("Error clearing currently playing song: %s\n", err.Error())
			}
			currentlyPlaying = nil
			sleep()
			continue
		}

		if listenbrainzCur := listenbrainzResponse.CurrentlyPlaying(); listenbrainzCur != nil {
			update := false // if we should send a request
			// a song is currently playing
			// no currently playing song, should update
			if currentlyPlaying == nil {
				update = true
				fmt.Printf("No song currently playing, setting to %+v\n", listenbrainzCur.TrackMetadata)
			} else {
				// check if song has changed
				if ListenChanged(currentlyPlaying, listenbrainzCur) {
					update = true
					fmt.Printf("Song changed, setting to %+v\n", listenbrainzCur.TrackMetadata)
				} else {
					debugPrint("Song has not changed, skipping")
				}
			}
			if update {
				currentlyPlaying = &currently_listening.SetListening{
					Artist:    listenbrainzCur.TrackMetadata.Artist_name,
					Album:     listenbrainzCur.TrackMetadata.Release_name,
					Title:     listenbrainzCur.TrackMetadata.Track_name,
					StartedAt: time.Now().Unix(),
				}

				// send currently playing song to server
				err = serverRequest(&currentlyPlaying, "set-listening")
				if err != nil {
					log.Fatalf("Error setting currently playing song: %s\n", err.Error())
				}
			}
		}
		sleep()
	}
}

func main() {
	app := &cli.App{
		Name:  "listenbrainz_client",
		Usage: "ListenBrainz client",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "password",
				Value:    "",
				Usage:    "Password to authenticate setting the currently playing song",
				Required: true,
				EnvVars:  []string{"CURRENTLY_LISTENING_PASSWORD"},
			},
			&cli.StringFlag{
				Name:     "listenbrainz-username",
				Value:    "",
				Usage:    "ListenBrainz username",
				Required: true,
				EnvVars:  []string{"LISTENBRAINZ_USERNAME"},
			},
			&cli.StringFlag{
				Name:  "server-url",
				Value: "http://127.0.0.1:3030",
				Usage: "URL of the server to send the currently playing song to",
			},
			&cli.IntFlag{
				Name:  "poll-interval",
				Value: 90,
				Usage: "Interval in seconds to poll ListenBrainz for currently playing song",
			},
			&cli.IntFlag{
				Name:  "poll-interval-when-playing",
				Value: 15,
				Usage: "Interval in seconds to poll ListenBrainz for currently playing song, when a song is playing",
			},
			&cli.BoolFlag{
				Name:  "debug",
				Value: false,
				Usage: "Enable debug logging",
			},
		},
		Action: func(c *cli.Context) error {
			pollListenbrainz(c.String("listenbrainz-username"), c.String("password"), c.String("server-url"), c.Bool("debug"), c.Int("poll-interval"), c.Int("poll-interval-when-playing"))
			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
