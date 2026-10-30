# Video metadata service

A Go API that stores video metadata in Postgres. It does not store video files.

## Run locally

```sh
docker compose up -d --remove-orphans
cp .env.example .env
set -a; source .env; set +a
go run .
```

Compose runs Postgres on port 5433.

## API

### `POST /videos`

```sh
curl -X POST localhost:8080/videos -d '{
  "title": "My video",
  "description": "Optional",
  "filename": "clip.mp4",
  "content_type": "video/mp4",
  "size_bytes": 1048576
}'
```

Returns `201` with the stored metadata. `content_type` must start with `video/`.

### `GET /videos`

```sh
curl localhost:8080/videos
```

Returns `200` with all videos, newest first.
