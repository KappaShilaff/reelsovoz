# reelsovoz

Telegram inline bot that sends TikTok and Instagram media into chats without saving final videos on disk.

Flow:

1. A user opens a private chat with the bot and sends `/start`.
2. In any Telegram chat, the user types `@your_bot_name https://...`.
3. The bot downloads/prepares the media, uploads it to the user's private bot chat as storage, and reuses Telegram `file_id`s for inline sends.
4. The source URL is kept only in the private storage chat with the bot. The friend/group chat receives only the media.

Supported input includes normal TikTok/Instagram videos and photo posts with music. Photo posts with music are converted to MP4.

## Quick Start With Docker Compose

Requirements:

- Docker with the Compose plugin
- A Telegram bot token from [@BotFather](https://t.me/BotFather)

Create and configure the bot in BotFather:

1. Create a bot with `/newbot`.
2. Enable inline mode with `/setinline`.
3. Enable inline feedback with `/setinlinefeedback`.
4. Set an inline placeholder such as `Paste a TikTok or Instagram URL`.

Run the service:

```sh
git clone https://github.com/KappaShilaff/reelsovoz.git
cd reelsovoz
cp .env.example .env
$EDITOR .env
docker compose up -d --build
```

At minimum, set this in `.env`:

```sh
TELEGRAM_BOT_TOKEN=123456:replace-me
```

Then open a private chat with your bot, send `/start`, and use it from any chat:

```text
@your_bot_name https://www.instagram.com/reel/...
@your_bot_name https://vt.tiktok.com/...
```

## Configuration

`.env.example` contains all supported variables.

Required:

- `TELEGRAM_BOT_TOKEN`: token from BotFather.

Usually leave these defaults:

- `USER_STORAGE_FILE=/data/reelsovoz-users.json`: persistent `/start` registrations.
- `YT_DLP_PATH=yt-dlp`
- `FFMPEG_PATH=ffmpeg`
- `DOWNLOAD_TIMEOUT=90s`
- `PREPARE_TIMEOUT=10m`
- `TELEGRAM_UPLOAD_RETRIES=3`
- `TELEGRAM_UPLOAD_TIMEOUT=120s`
- `MAX_VIDEO_BYTES=50331648`
- `HEALTH_ADDR=:8000`

Optional:

- `INSTAGRAM_COOKIES_B64`: base64-encoded Netscape cookies file for Instagram.
- `INSTAGRAM_COOKIES_FILE`: path to a Netscape cookies file inside the container.
- `TELEGRAM_STORAGE_CHAT_ID`: legacy single storage chat fallback. Most installs should use `/start` per user instead.

The Compose file stores state in the Docker volume `reelsovoz-data`, mounted at `/data`.

## Instagram Cookies

Some Instagram photo posts with music do not expose audio metadata to logged-out requests. For those posts, export Instagram cookies in Netscape format and provide them to the bot.

Recommended Compose setup:

```sh
base64 -w0 instagram_cookies.txt
```

Put the output into `.env`:

```sh
INSTAGRAM_COOKIES_B64=base64-output-here
```

Do not commit cookies or real `.env` files.

## Operations

View logs:

```sh
docker compose logs -f
```

Restart:

```sh
docker compose restart
```

Update:

```sh
git pull
docker compose up -d --build
```

Stop:

```sh
docker compose down
```

Remove persisted `/start` registrations:

```sh
docker compose down -v
```

Health endpoints:

- `http://127.0.0.1:8000/healthz`
- `http://127.0.0.1:8000/readyz`

## Local Development

Install `yt-dlp` and `ffmpeg`, then run:

```sh
cp .env.example .env
$EDITOR .env
set -a
. ./.env
set +a
go run ./cmd/reelsovoz
```

Checks:

```sh
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go vet ./...
```
