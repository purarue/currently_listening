A personal Websocket based currently playing web server. Generally, this follows something like:

```
/**************************************************************************************/
/* client code which polls applications/websites for music I'm currently listening to */
/**************************************************************************************/
                                       |
                                       |
                                       ▼
/*********************************************************************************/
/*                                  main server                                  */
/* accepts requests from other client code here to update currently listening to */
/*               broadcasts currently listening data on a websocket              */
/*********************************************************************************/
                                       |
                                       |
                                       ▼
       /***************************************************************/
       /* enduser/applications which consume the websocket to display */
       /*       e.g. as part of my website/discord presence           */
       /***************************************************************/
```

As an example, I have [some react code](https://github.com/purarue/glue/blob/master/assets/frontend/currently_listening.tsx) that connects to the main server here and displays it on my website. That appears on [my website](https://sean.fish) in the bottom left if I'm currently listening to something:

https://user-images.githubusercontent.com/7804791/215688320-c7adb7cb-299e-46a4-afd4-8abd9687a868.mp4

I also use this to set my discord presence:

![demo discord image](https://github.com/purarue/currently_listening/blob/main/.github/discord.png?raw=true)

## Install

Requires `python3.10+` (for local data processing/clients) and `go` (for the remote websocket server)

### `server`

The main server `./server/main.go` can be built with:

```bash
git clone https://github.com/purarue/currently_listening
cd currently_listening
go build -o currently_listening_server ./server/main.go
cp ./currently_listening_server ~/.local/bin
```

Run `currently_listening_server`:

```
GLOBAL OPTIONS:
   --port value         Port to listen on (default: 3030)
   --password value     Password to authenticate setting the currently listening song [$CURRENTLY_LISTENING_PASSWORD]
   --stale-after value  Number of seconds after which the currently listening song is considered stale, and will be cleared. Typically, this should be cleared by the client, but this is a fallback to prevent stale state from remaining for long periods of time (default: 3600)
   --help, -h           show help
```

Set the `CURRENTLY_LISTENING_PASSWORD` environment variable to authenticate `POST` requests (so that only you can set what music you're listening to)

Accepts `POST` requests from other clients here to set/clear the currently playing song, and provides the `/ws` endpoint which broadcasts to other clients whenever there are changes

If you want to be able to access the websocket from other devices/on a website, you need to host this on a server somewhere public.

I do so on my server with `nginx` under the `/currently_listening` path:

```conf
location /currently_listening/ {
  add_header "Access-Control-Allow-Origin"  *;
  proxy_http_version 1.1;
  proxy_set_header X-Cluster-Client-Ip $remote_addr;
  proxy_set_header Upgrade $http_upgrade;
  proxy_set_header Connection "upgrade";
  proxy_pass http://127.0.0.1:3030/;
}
```

---

The rest of the code here are clients which set the song I'm currently listening to, or which consume the websocket endpoint in some way (e.g., to set my discord presence)

### `listenbrainz_client`

Install (you can also use the `Makefile` here to build both `go` tools):

```bash
git clone https://github.com/purarue/currently_listening
cd currently_listening
go build -o listenbrainz_client_poll ./listenbrainz_client/main.go
cp ./listenbrainz_client_poll ~/.local/bin
```

This polls the `playing-now` endpoint at [ListenBrainz](https://listenbrainz.org/) (like a open-source last-fm) every few seconds to fetch what I'm currently listening to.

Whenever it detects currently playing music/music finishes playing, it sends a request to `./server/main.go`. Similar to Lastfm, ListenBrainz is updated by scrobblers, like [Pano Scrobbler](https://play.google.com/store/apps/details?id=com.arn.scrobble&hl=en_US&gl=US) on my phone, or [WebScrobbler](https://web-scrobbler.com/) in my browser

Run `listenbrainz_client_poll`:

```
GLOBAL OPTIONS:
   --password value                    Password to authenticate setting the currently playing song [$CURRENTLY_LISTENING_PASSWORD]
   --listenbrainz-username value       ListenBrainz username [$LISTENBRAINZ_USERNAME]
   --server-url value                  URL of the server to send the currently playing song to (default: "http://127.0.0.1:3030")
   --poll-interval value               Interval in seconds to poll ListenBrainz for currently playing song (default: 90)
   --poll-interval-when-playing value  Interval in seconds to poll ListenBrainz for currently playing song, when a song is playing (default: 15)
   --debug                             Enable debug logging (default: false)
   --help, -h                          show help
```

This could run either on your local machine or remotely, but I prefer remotely as it means its always active -- even if I'm out somewhere listening on my phone it still works.

### `mpv_history_daemon`

This requires:

- <https://github.com/purarue/mpv-history-daemon>
- <https://github.com/purarue/mpv-sockets>

This is a pretty complex source with lots of moving parts, so to summarize:

```
                            /*********************/
                            /* mpv (application) */
                            /*********************/
                                       |
                                       ▼
                   /****************************************/
                   /*      mpv_sockets wrapper script      */
                   /* launches mpv with unique IPC sockets */
                   /****************************************/
                                       |
                                       ▼
       /***************************************************************/
       /*                      mpv_history_daemon                     */
       /*            connects to active mpv IPC sockets and           */
       /*      saves data to local JSON files. when launched with     */
       /* the custom SocketDataServer class installed here, this also */
       /*   sends the JSON to a local currently_listening_py server   */
       /***************************************************************/
                                       |
                                       ▼
  /*************************************************************************/
  /*              currently_listening_py server (run locally)              */
  /*          parse/filter the raw JSON from mpv_history_daemon            */
  /*    optionally locates/caches/sends a thumbnail of the current song    */
  /*************************************************************************/
                                       |
                                       ▼
     /********************************************************************/
     /*                   main server (server/main.go)                   */
     /* receives updates whenever mpv song changes/mpv is paused/resumed */
     /********************************************************************/
                                       |
                                       ▼
                 /*******************************************/
                 /* clients receive broadcasts on websocket */
                 /*******************************************/
```

To install the python library/server here:

```bash
git clone https://github.com/purarue/currently_listening
cd currently_listening/currently_listening_py
python3 -m pip install .
```

To run, first start the `currently_listening_py` server, e.g.:

`currently_listening_py server --server-url https://.../currently_listening`

```
Usage: currently_listening_py server [OPTIONS]

Options:
  --server-url TEXT               remote server url  [default: http://127.0.0.1:3030]
  --send-images / --no-send-images
                                  if available, send base64 encoded images to the server. This caches compressed
                                  thumbnails to a local cache dir
  --port INTEGER                  local port to host on
  --matcher-config-file FILE      path to a matcher config file
  --help                          Show this message and exit.
```

Then, run the `mpv_history_daemon` with the custom `SocketDataServer` class installed here

`mpv_history_daemon_restart ~/data/mpv --socket-class-qualname 'currently_listening_py.socket_data.SocketDataServer'`

This still saves all the data to `~/data/mpv`, in addition to `POST`ing the currently playing song to the local `currently_listening_py server` for further processing

If there is a file named `.nsfw` in the same folder as the currently playing song, it will blur the cached thumbnail before sending it to the server

## Filtering

By default, this will check the `mpv` metadata for the `artist` and `album` tags and `title` tags before sending to the server. It also prevents livestreams, and paths like `/tmp` or `/dev`.

If you'd like to further customize that to allow/disallow certain paths/extensions, you can pass a JSON file as `--matcher-config-file` to the `server`, with contents like [`matcher.json`](./client_examples/matcher.json)

For more info on the options: `python3 -c 'import mpv_history_daemon.utils; help(mpv_history_daemon.utils.MediaAllowed)'`

Mine is configured [here](https://github.com/purarue/my_feed/blob/b5dc3a9970ba38bef5a531bc9e32d42541229be1/src/my_feed/sources/mpv.py#L254-L263) in [`my_feed`](https://github.com/purarue/my_feed)

#### print

The `currently_listening_py` command also includes a `print` command, which can print the currently playing song as text, JSON or an image:

```
$ currently_listening_py print --server-url 'wss://sean.fish/currently_listening/ws' -o text
Main Theme - Amynedd (16-Bit Adventure)
```

It saves the image to `~/.cache/currently-listening-py/currently_listening.jpg`.

I use [kitty](https://sw.kovidgoyal.net/kitty/), which means I can print images in the terminal, so I have a shell function like:

```bash
curplaying() {
    [[ "$TERM" != "xterm-kitty" ]] && return 1
    python3 -m currently_listening_py print --server-url 'wss://sean.fish/currently_listening/ws' -o image 2> /dev/null && kitty icat --align=left ~/.cache/currently-listening-py/currently_listening.jpg
}
```

![demo terminal image](https://github.com/purarue/currently_listening/blob/main/.github/interminal.png?raw=true)

### `discord-presence`

To setup your client ID, see [pypresence](https://qwertyquerty.github.io/pypresence/html/info/quickstart.html) docs, and set the `PRESENCE_CLIENT_ID` environment variable with your applications `ClientID`.

This must be run on your computer which the `discord` application active to connect with RPC, e.g.:

`currently_listening_py discord-presence --server-url wss://sean.fish/currently_listening/ws`

```
Usage: currently_listening_py discord-presence [OPTIONS]

Options:
  --server-url TEXT               remote server url  [default:
                                  ws://127.0.0.1:3030/ws]
  --image-url TEXT                endpoint for currently playing image url
                                  [default: http://127.0.0.1:3030/currently-
                                  listening-image]
  -d, --discord-client-id TEXT    Discord client id for setting my presence
                                  [env var: PRESENCE_CLIENT_ID]
  -D, --discord-rpc-wait-time INTEGER RANGE
                                  Interval in seconds to wait between discord
                                  rpc requests  [x>=15]
  -p, --websocket-poll-interval INTEGER
                                  Interval in seconds to poll the websocket
                                  for updates, to make sure failed RPC
                                  requests dont lead to stale presence
  --help                          Show this message and exit.
```

To comply with the discord RPC rate limit, this only updates to the most recent request every ~20 seconds

![demo discord image](https://github.com/purarue/currently_listening/blob/main/.github/discord.png?raw=true)

### Adding new sources

The `mpv`/`listenbrainz` sources here are just the ones that are most relevant to me, the main server here can accept `POST` requests from any tool/daemon you write.

The two relevant endpoints (which both require `password`: `CURRENTLY_LISTENING_PASSWORD` as a header to authenticate):

`/set-listening`, with a `POST` body that looks like:

```yaml
{
  "artist": "artist name",
  "album": "album name", # can be empty string if no album known
  "title": "title/track name",
  "started_at": 1675146416, # epoch time
  "base64_image": "...", # base64 encoded image
}
```

If a `base64_image` is provided by the client, its sent back as part of the response. This also includes an image endpoint `/currently-listening-image/`, which returns the image for the song that's currently playing, if `base64_image` was set.

If requesting this from something which might cache this image, can add additional random text as part of the path, e.g.,: `/currently-listening-image/JkFJQ0hJTkdfSU1BR0U9FgF49kKFLASMRIEJKMW2340`

`/clear-listening` which clears the current song from memory (in other words, I finished listening to the song), with `POST` body like:

```yaml
{ "ended_at": 1675190002 }
```

Whenever either of those are hit with a `POST` request, it broadcasts to any currently connected websockets on `/ws`

`currently_listening_py` includes a `print` command which sends the `currently-listening` message to websocket:

`$ python3 -m currently_listening_py print --server-url 'wss://sean.fish/currently_listening/ws' | jq`

```json
{
  "msg_type": "currently-listening",
  "data": {
    "song": {
      "artist": "Kendrick Lamar",
      "album": "To Pimp a Butterfly",
      "title": "Momma",
      "started_at": 1675146504
    },
    "playing": true
  }
}
```

If `playing` is `false`, `song` is `null`

---

Some basic python to connect to the server and receive broadcasts

```python
import os
import sys
import json
import asyncio

from websockets.client import connect
from websockets.exceptions import ConnectionClosed


async def _websocket_loop(server_url: str) -> None:
    async for websocket in connect(server_url):
        await websocket.send("currently-listening")
        try:
            while True:
                message = await websocket.recv()
                print(json.loads(message))
        except ConnectionClosed:
            print("Connection closed", file=sys.stderr)
            await asyncio.sleep(1)
            continue


asyncio.run(_websocket_loop(os.environ.get("WEBSOCKET_URL", "ws://127.0.0.1:3030/ws")))
```

or javascript:

```javascript
const websocketUrl = Deno.env.get("WEBSOCKET_URL") || "ws://127.0.0.1:3030/ws";

function connect() {
  let ws = new WebSocket(websocketUrl);

  ws.onopen = () => {
    console.log("Connected to websocket server");
    ws.send("currently-listening");
  };

  ws.onmessage = (event) => {
    console.log(
      "Received message from websocket server:",
      JSON.parse(event?.data ?? "{}", null, 2)
    );
  };

  ws.onclose = () => {
    console.log("Disconnected from websocket server");
    // reconnect
    setTimeout(() => {
      console.log("Reconnecting to websocket server");
      connect();
    }, 1000);
  };
}

connect();
```
