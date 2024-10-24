import os
import base64
from typing import Optional, Any, List, Tuple, Dict
from pathlib import Path

import uvicorn  # type: ignore[import]
from fastapi import FastAPI
import requests
import platformdirs
from more_itertools import last
from fastapi import APIRouter
from mpv_history_daemon.events import _read_event_stream, Media
from mpv_history_daemon.utils import MediaAllowed, music_parse_metadata_from_blob

from logzero import logger  # type: ignore[import]

from .socket_data import SocketBody, SetListening, ClearListening


# this defines what media is allowed to be sent to the server
# if this is None, all media is allowed (as long as it has a title, artist and album)
#
# by default, it ignores some directories like /dev/ and /tmp/
# https://github.com/purarue/mpv-history-daemon/blob/master/mpv_history_daemon/utils.py
GLOBAL_MATCHER: Optional[MediaAllowed] = None

# personally, I configure this in my_feed, so I shall just reuse it here
# https://github.com/purarue/my_feed/blob/b5dc3a9970ba38bef5a531bc9e32d42541229be1/src/my_feed/sources/mpv.py#L254-L263
try:
    from my_feed.sources.mpv import matcher as my_feed_matcher  # type: ignore

    assert isinstance(my_feed_matcher, MediaAllowed)
    assert my_feed_matcher._logger is not None
    GLOBAL_MATCHER = my_feed_matcher
    del my_feed_matcher
except Exception as e:
    logger.debug(f"Failed to import custom my_feed matcher: {e}", exc_info=False)


class SocketDataManager:
    def __init__(
        self,
        remote_server: str,
        server_password: str,
        cache_images: bool,
        matcher: MediaAllowed,
    ) -> None:
        self.currently_listening: Optional[SetListening] = None
        self.is_playing = False
        self.remote_server_url = remote_server
        self.server_password = server_password
        self.cache_images = cache_images
        self.cache_image_dir = Path(
            platformdirs.user_cache_dir(appname="currently-listening-py")
        )
        self.matcher = matcher

    def _post_to_server(
        self, path: str, body: SetListening | ClearListening | None = None
    ) -> None:
        data: Dict[str, Any] = body.model_dump() if body is not None else {}
        try:
            resp = requests.post(
                f"{self.remote_server_url}/{path}",
                json=data,
                headers={"password": self.server_password},
            )
        except requests.RequestException as e:
            logger.error(f"Failed to post to {path}: {e}", exc_info=True)
            return
        if resp.status_code != 200:
            logger.warning(
                f"Got status code {resp.status_code} from {path}: {resp.text}"
            )

    def update_currently_listening(
        self, body: SetListening | ClearListening, is_playing: bool
    ) -> None:
        if isinstance(body, ClearListening):
            if not is_playing and self.is_playing:
                logger.debug("Clearing currently listening")
                self.currently_listening = None
                self.is_playing = False
                self._post_to_server(
                    path="clear-listening",
                    body=body,
                )
                return
        else:
            if is_playing and self.currently_listening != body:
                logger.debug(
                    f"Setting currently listening to: {body.artist=} {body.title=} {body.album=}"
                )
                self.currently_listening = body
                self.is_playing = True
                self._post_to_server(
                    path="set-listening",
                    body=body,
                )

    COVERS = [
        "cover.jpg",
        "cover.png",
        "Folder.jpg",
        "Folder.png",
        "thumb.jpg",
    ]

    @classmethod
    def get_cover_art(cls, media: Media) -> Optional[Path]:
        collection_dir = os.path.dirname(media.path)
        for cover in cls.COVERS:
            cover_path = os.path.join(collection_dir, cover)
            if os.path.exists(cover_path):
                return Path(cover_path)
        return None

    @classmethod
    def cache_compressed_cover_art(
        cls, image: Path, save_to: Path, blur_image: bool
    ) -> Path:
        from PIL import Image, ImageFilter  # type: ignore[import]

        with open(image, "rb") as f:
            img = Image.open(f)
            img.thumbnail((100, 100))
            if blur_image:
                img = img.filter(ImageFilter.GaussianBlur(radius=5))
            with open(save_to, "wb") as tf:
                img.save(tf, "JPEG")
            logger.debug(f"Saved compressed cover art to {save_to}")
        return save_to

    def get_compressed_cover_art(self, media: Media) -> Optional[str]:
        """
        A bit complicated but when is caching not

        Search for cover art in the same directory as the media,
        and cache it to a directory in the users cache directory

        If the media file is marked as nsfw (has a .nsfw file in the same directory),
        blur the image before sending it to the server

        If the cache is older than the source, re-cache
        If the cache is older than the nsfw marker, re-cache
        """
        cover_art = self.get_cover_art(media)
        if cover_art is None:
            logger.debug(f"No cover art found for {media.path} using {self.COVERS=}")
            return None

        # check if Ive marked this album having nsfw album art (in case I wouldn't want random nsfw images in discord presence/on my website)
        is_nsfw_marker: Path = cover_art.parent.joinpath(".nsfw")
        nsfw_mod_time: Optional[float] = (
            is_nsfw_marker.stat().st_mtime if is_nsfw_marker.exists() else None
        )
        if nsfw_mod_time is not None:
            logger.debug(f"nsfw modification time {nsfw_mod_time=}")
        else:
            logger.debug(f"No nsfw marker ({is_nsfw_marker}) found for {cover_art=}")

        # exclude first part of path '/'
        # and last part of path (song filename)
        cache_dir_target = self.cache_image_dir / os.path.join(
            *Path(media.path).absolute().parts[1:-1]
        )
        logger.debug(f"Found source art: {cover_art=}")
        cache_target: Path = (cache_dir_target / cover_art.name).with_suffix(".jpg")
        cache_target_exists: bool = cache_target.exists()
        cache_target_mod_time: Optional[float] = (
            cache_target.stat().st_mtime if cache_target_exists else None
        )

        # if the nsfw marker is newer than the cache, re-cache (I might have just added the marker)
        nsfw_force_recompute = (
            nsfw_mod_time is not None
            and cache_target_mod_time is not None
            and nsfw_mod_time > cache_target_mod_time
        )
        if nsfw_force_recompute and cache_target_exists:
            logger.debug("nsfw marker is newer than cache, refreshing image as blurred")

        # check if cache target is older than the source
        # if so, re-cache
        cache_target_expired = False
        if cache_target_exists:
            cache_target_expired = (
                cache_target.stat().st_mtime < cover_art.stat().st_mtime
            )

        if cache_target_expired:
            logger.debug(
                f"Cache target {cache_target=} is older than source {cover_art=}, refreshing"
            )

        if cache_target_expired or nsfw_force_recompute:
            logger.debug(f"Removing file {cache_target=}...")
            cache_target.unlink()
            cache_target_exists = False

        if not cache_target_exists:
            logger.debug(f"No cached cover art found: {cache_target=}")
            cache_target.parent.mkdir(parents=True, exist_ok=True)
            try:
                self.cache_compressed_cover_art(
                    cover_art,
                    save_to=cache_target,
                    blur_image=nsfw_mod_time is not None,
                )
            except Exception as e:
                logger.error(f"Failed to cache cover art: {e}", exc_info=True)
                return None

        # do a re-check here for exists in case the above failed
        if cache_target.exists():
            logger.debug(f"Found cached cover art: {cache_target=}")
            # load image as base64
            with open(cache_target, "rb") as f:
                return base64.b64encode(f.read()).decode("utf-8")

        return None

    @staticmethod
    def _has_metadata(m: Media) -> Optional[Tuple[str, str, str]]:
        return music_parse_metadata_from_blob(m.metadata)

    def process_currently_listening(self, body: SocketBody) -> None:
        # allow_if_playing_for=0 means every song is allowed, since
        # current time is always larger than the mpv start time
        data: List[Media] = list(
            _read_event_stream(
                body.events, filename=body.filename, allow_if_playing_for=0
            )
        )
        if len(data) == 0:
            return
        data.sort(key=lambda x: x.start_time)
        current = last(data)

        if not self.matcher.is_allowed(current):
            logger.info(f"Media not allowed: {current}")
            return

        metadata = self.__class__._has_metadata(current)
        if metadata is None:
            logger.info(f"Media doesn't have enough metadata: {current.metadata}")
            return

        title, album, artist = metadata

        cover_art: Optional[str] = None
        if self.cache_images and body.is_playing:
            cover_art = self.get_compressed_cover_art(current)

        sendbody: SetListening | ClearListening
        if body.is_playing:
            sendbody = SetListening(
                title=title,
                artist=artist,
                album=album,
                started_at=int(current.start_time.timestamp()),
                base64_image=cover_art or "",
            )
        else:
            sendbody = ClearListening(
                ended_at=int(current.start_time.timestamp()),
            )
        self.update_currently_listening(
            body=sendbody,
            is_playing=body.is_playing,
        )


manager: Optional[SocketDataManager] = None


def setup_config(
    remote_server: str,
    server_password: str,
    cache_images: bool,
    use_matcher: Optional[MediaAllowed],
) -> None:
    global manager

    matcher: Optional[MediaAllowed]
    if use_matcher is not None:
        matcher = use_matcher
    elif GLOBAL_MATCHER is not None:
        matcher = GLOBAL_MATCHER
    else:
        matcher = MediaAllowed()

    # if matcher doesn't have a logger, set it to the default logger here
    # only create matcher if it doesn't exist/user hasn't specified one
    if matcher._logger is None:
        matcher._logger = logger

    logger.debug(f"Using matcher: {matcher}")

    manager = SocketDataManager(remote_server, server_password, cache_images, matcher)


def create_router() -> APIRouter:
    router = APIRouter()

    @router.post("/socket_data")
    def _socket_data(body: SocketBody) -> None:
        assert manager is not None
        manager.process_currently_listening(body)

    return router


def server(
    *,
    port: int,
    remote_server: str,
    server_password: str,
    cache_images: bool,
    use_matcher: Optional[MediaAllowed] = None,
    debug: bool = True,
) -> None:
    app = FastAPI()

    @app.get("/ping")
    def _ping() -> str:
        return "pong"

    setup_config(remote_server, server_password, cache_images, use_matcher)

    app.include_router(create_router())

    loglevel = "debug" if debug else "info"

    uvicorn.run(app, host="127.0.0.1", port=port, log_level=loglevel)
