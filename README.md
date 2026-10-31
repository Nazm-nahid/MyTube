# Video upload service

A Go API that stores video metadata in Postgres and gives the client a signed
Google Cloud Storage URL. The file goes straight from the client to the bucket,
so video bytes never pass through the API server.

## Run locally

Complete [Google Cloud Storage setup](#google-cloud-storage-setup) first.

```sh
docker compose up -d --remove-orphans
cp .env.example .env
set -a; source .env; set +a
go run .
```

Open http://localhost:8080 for the video list and player, or
http://localhost:8080/upload.html for the upload screen. Compose runs Postgres
on port 5433.

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

Returns `201` with `video`, `upload_url`, `upload_headers` and `expires_at`.
`content_type` must start with `video/`, and `size_bytes` can be at most 5 GiB.

Then upload to the returned URL with every header from `upload_headers`. They
are part of the signature, and they make Cloud Storage reject a file of a
different type or size.

```sh
curl -X PUT \
  -H 'Content-Type: video/mp4' \
  -H 'x-goog-content-length-range: 1048576,1048576' \
  --data-binary @clip.mp4 "$UPLOAD_URL"
```

### `GET /videos`

```sh
curl localhost:8080/videos
```

Returns `200` with all videos, newest first.

### `GET /videos/{id}/stream`

```sh
curl localhost:8080/videos/$ID/stream
```

Returns `200` with `stream_url`, a signed Cloud Storage URL that lasts 6 hours,
and `expires_at`. Use `stream_url` as the player's source. Cloud Storage serves
the range requests players use to seek. Returns `404` if the video was never
uploaded. Files play as uploaded, so a browser can only play formats it
supports.

## Google Cloud Storage setup

The API signs URLs as a service account, so no key file is needed. Replace
`<you@example.com>` with your Google account, then grant the access once:

```sh
PROJECT=project-2964aeaf-bfcb-4139-a96
BUCKET=project-2964aeaf-bfcb-4139-a96-videos
SA=video-service@$PROJECT.iam.gserviceaccount.com

# Lets the service account write and read objects.
gcloud storage buckets add-iam-policy-binding gs://$BUCKET \
  --member=serviceAccount:$SA --role=roles/storage.objectAdmin

# Lets the service account sign URLs.
gcloud iam service-accounts add-iam-policy-binding $SA \
  --member=serviceAccount:$SA --role=roles/iam.serviceAccountTokenCreator

# Lets you act as the service account during local development.
gcloud iam service-accounts add-iam-policy-binding $SA \
  --member=user:<you@example.com> --role=roles/iam.serviceAccountTokenCreator

gcloud auth application-default login --impersonate-service-account=$SA
```

For browser uploads, save this as `cors.json`:

```json
[{
  "origin": ["http://localhost:8080"],
  "method": ["PUT"],
  "responseHeader": ["Content-Type", "x-goog-content-length-range"],
  "maxAgeSeconds": 3600
}]
```

```sh
gcloud storage buckets update gs://$BUCKET --cors-file=cors.json
```
