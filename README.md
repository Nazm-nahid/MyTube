# BanglaTube

A small YouTube-style service written in Go. It stores video metadata in
Postgres, uploads files directly to a private Google Cloud Storage (GCS) bucket,
converts each upload to HLS, and plays it in the browser or an external media
player.

Video bytes never pass through the API server. The API only stores metadata,
signs URLs and serves small HLS playlist files.

## Contents

- [Architecture](#architecture)
- [How the project was built](#how-the-project-was-built)
- [Project structure](#project-structure)
- [Run locally](#run-locally)
- [Web pages](#web-pages)
- [API](#api)
- [Data model](#data-model)
- [Configuration](#configuration)
- [Bucket layout](#bucket-layout)
- [Google Cloud setup](#google-cloud-setup)
- [Play in an external media player](#play-in-an-external-media-player)
- [Design decisions](#design-decisions)
- [Limitations](#limitations)
- [Troubleshooting](#troubleshooting)
- [Current status](#current-status)

## Architecture

### Upload

```
Browser                API              Postgres            GCS bucket
   |-- POST /videos ---->|                    |                  |
   |                     |-- sign PUT URL     |                  |
   |                     |-- insert metadata->|                  |
   |<-- upload_url ------|                    |                  |
   |-- PUT file directly ------------------------------------->  |
```

### Transcoding

```
GCS bucket --(object created)--> Eventarc trigger --> Cloud Workflow
                                 video-uploaded       video-transcode
                                                           |
                                                           v
GCS bucket <--(HLS files in transcoded/<id>/)----- Transcoder API job
                                                   preset/web-hd
```

### Playback

```
Browser                         API                     Cloud CDN / GCS bucket
   |-- GET /videos/{id}/stream -->|                         |
   |<-- stream_url, format -------|                         |
   |-- GET .../hls/manifest.m3u8->|-- read playlist ------->|
   |<-- playlist -----------------|                         |
   |-- GET .../hls/<segment> ---->|-- sign CDN URL          |
   |<-- 302 to signed URL --------|                         |
   |-- GET signed URL --------------------------------->    |
   |<-- video bytes, from cache or the bucket --------------|
```

Without `CDN_URL`, as in local development, the API signs GCS URLs instead and
the browser reads directly from the bucket.

## How the project was built

The service started as a minimal metadata API and grew in five stages.

### Stage 1: Minimal metadata API

The first version was reduced to two endpoints:

- `POST /videos` saved a video's title, description, filename, content type and
  size in Postgres.
- `GET /videos` listed the saved videos, newest first.

No file was stored. The earlier S3, MinIO, VersityGW and fake-gcs-server code was
removed, along with the single-video and upload-completion endpoints. The
`videos` table dropped its old upload columns (`s3_key`, `object_key`, `status`
and `uploaded_at`) because they no longer applied.

### Stage 2: Web pages

Two basic HTML pages were added and embedded in the Go binary:

- `web/index.html` shows the video list.
- `web/upload.html` is the upload screen.

### Stage 3: Direct upload with a signed URL

`POST /videos` began returning a signed `PUT` URL for the real GCS bucket.

- `gcs.go` was added to create signed URLs with the official GCS client.
- The URL lasts 15 minutes and works only for the signed file type and exact size.
- `video-service` was set up as the service account that signs URLs, so no key
  file is needed.
- The upload screen began sending the real file straight to GCS and showing a
  progress bar.
- A bucket CORS rule was added so the browser could upload from
  `http://localhost:8080`.
- The 5 GiB size limit was added back.

Metadata is saved before the upload starts. Nothing confirms that the upload
finished, which is listed under [Limitations](#limitations).

### Stage 4: Video playback

`GET /videos/{id}/stream` and `web/watch.html` were added. The list page gained a
**Play** link.

The endpoint first redirected to a signed `GET` URL. It was then changed to return
that URL as JSON, so `watch.html` makes an API call when it loads and gives the
result to the player. GCS answers the player's range requests, which is what
makes seeking work. The signed URL lasts 6 hours so it outlasts a long video.

### Stage 5: HLS transcoding and streaming

GCS does not convert files by itself, so the bucket only held original uploads.
This stage added automatic conversion:

- An Eventarc trigger, `video-uploaded`, fires when an object is created.
- A Cloud Workflow, `video-transcode`, ignores anything outside `videos/` and
  starts a Transcoder API job for each upload.
- The job uses `preset/web-hd` and writes 360p and 720p HLS to
  `transcoded/<id>/`.

The workflow was chosen over a Cloud Run function because it needs no extra Go
packages or deployed application code.

An HLS video is many files, and one signed URL only covers one file. To keep the
bucket private, `GET /videos/{id}/hls/{path}` was added. It serves playlists from
the API and redirects each segment to its own signed URL.

`GET /videos/{id}/stream` now returns `format: "hls"` when `manifest.m3u8` exists,
and otherwise returns `format: "file"` with the original file. A video can
therefore be tried while it is still converting. No status column or polling is
needed.

`watch.html` plays HLS with `hls.js`, loaded from a CDN.

### Playback fix

Chrome 156 reports native HLS support, so the first HLS player code skipped
`hls.js` and used Chrome's built-in player. After a network loss, that player
stopped and the page wrongly said the browser could not play the format.

`watch.html` now uses `hls.js` wherever the browser supports it and keeps native
playback as the fallback. It shows `Connection lost. Retrying…`, retries every 3
seconds, and recovers from media errors.

## Project structure

| Path | Purpose |
|---|---|
| `main.go` | Reads settings, connects to Postgres and GCS, runs the server |
| `handler.go` | HTTP routes, validation and JSON responses |
| `store.go` | Postgres queries |
| `gcs.go` | Signed URLs and object reads |
| `schema.sql` | Table definition, applied at startup |
| `transcode.workflow.yaml` | Cloud Workflow that starts transcoding jobs |
| `web/index.html` | Video list |
| `web/upload.html` | Upload screen |
| `web/watch.html` | Video player |
| `web/app.css` | Shared styles for the web pages |
| `docker-compose.yml` | Local Postgres |
| `.env.example` | Example settings |

## Run locally

Requirements are Go 1.26, Docker, the `gcloud` CLI, and completed
[Google Cloud setup](#google-cloud-setup).

```sh
docker compose up -d --remove-orphans
cp .env.example .env
set -a; source .env; set +a
go run .
```

The app does not read `.env` by itself, so run the `source` line once in each new
terminal. Compose runs Postgres on port 5433. Web pages are embedded in the binary,
so restart the server after changing them.

## Web pages

| URL | Purpose |
|---|---|
| http://localhost:8080 | Video list with **Play** links |
| http://localhost:8080/upload.html | Upload screen with a progress bar |
| http://localhost:8080/watch.html?id=VIDEO_ID | Video player |

## API

### `POST /videos`

Saves metadata and returns a signed upload URL.

```sh
curl -X POST localhost:8080/videos -d '{
  "title": "My video",
  "description": "Optional",
  "filename": "clip.mp4",
  "content_type": "video/mp4",
  "size_bytes": 1048576
}'
```

Returns `201`:

```json
{
  "video": {
    "id": "13608b29-ecd7-4c55-afd8-2ac86baa9154",
    "title": "My video",
    "description": "Optional",
    "filename": "clip.mp4",
    "content_type": "video/mp4",
    "size_bytes": 1048576,
    "created_at": "2026-10-31T13:08:09+06:00"
  },
  "upload_url": "https://storage.googleapis.com/...",
  "upload_headers": {
    "Content-Type": "video/mp4",
    "x-goog-content-length-range": "1048576,1048576"
  },
  "expires_at": "2026-10-31T13:23:09+06:00"
}
```

Returns `400` when `title` or `filename` is empty, `content_type` does not start
with `video/`, or `size_bytes` is not between 1 byte and 5 GiB.

### `PUT {upload_url}`

Uploads the file directly to GCS. Send every header from `upload_headers`. They
are part of the signature, and they make GCS reject a file of a different type or
size.

```sh
curl -X PUT \
  -H 'Content-Type: video/mp4' \
  -H 'x-goog-content-length-range: 1048576,1048576' \
  --data-binary @clip.mp4 "$UPLOAD_URL"
```

### `GET /videos`

Returns `200` with all videos, newest first.

```sh
curl localhost:8080/videos
```

### `GET /videos/{id}/stream`

Returns the URL to play.

```sh
curl localhost:8080/videos/$ID/stream
```

After transcoding:

```json
{
  "stream_url": "/videos/13608b29-ecd7-4c55-afd8-2ac86baa9154/hls/manifest.m3u8",
  "format": "hls"
}
```

Before transcoding finishes:

```json
{
  "stream_url": "https://storage.googleapis.com/...",
  "format": "file",
  "expires_at": "2026-10-31T19:03:51+06:00"
}
```

Returns `404` if the video was never uploaded.

### `GET /videos/{id}/hls/{path}`

Used by HLS players. `.m3u8` playlists are served by the API with
`application/vnd.apple.mpegurl`. Other files redirect with `302` to a signed CDN
URL, or a signed GCS URL when `CDN_URL` is not set.

## Data model

`videos` table:

| Column | Type | Notes |
|---|---|---|
| `id` | `UUID` | Primary key |
| `title` | `TEXT` | Required |
| `description` | `TEXT` | Defaults to empty |
| `filename` | `TEXT` | Original filename |
| `content_type` | `TEXT` | Must start with `video/` |
| `size_bytes` | `BIGINT` | Exact upload size |
| `created_at` | `TIMESTAMPTZ` | Set by Postgres |

Object paths come from the video ID, so they are not stored in the table.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `ADDR` | `:8080` | Server address |
| `DATABASE_URL` | Local Compose database | Postgres connection |
| `GCS_BUCKET` | None, required | Bucket name |
| `UPLOAD_URL_TTL` | `15m` | How long upload URLs last |
| `CDN_URL` | None | CDN base URL. Without it, playback uses signed GCS URLs |
| `CDN_KEY_NAME` | None, required with `CDN_URL` | Signing key name on the backend bucket |
| `CDN_KEY` | None, required with `CDN_URL` | URL-safe base64 signing key. Keep it secret |

## Bucket layout

| Path | Contents |
|---|---|
| `videos/<id>` | Original upload, which triggers transcoding |
| `transcoded/<id>/manifest.m3u8` | Main HLS playlist |
| `transcoded/<id>/media-sd.m3u8`, `media-hd.m3u8` | Playlists for 360p and 720p |
| `transcoded/<id>/media-sd*.ts`, `media-hd*.ts` | HLS video |
| `transcoded/<id>/manifest.mpd`, `*.m4s`, `sd.mp4`, `hd.mp4` | Other `preset/web-hd` output, unused by the app |

The `.ts` files are read in parts using `EXT-X-BYTERANGE`, so one file can hold
many HLS segments.

## Google Cloud setup

Everything runs in project `project-2964aeaf-bfcb-4139-a96`, region
`asia-south1`.

| Resource | Name | Purpose |
|---|---|---|
| Bucket | `project-2964aeaf-bfcb-4139-a96-videos` | Private video storage |
| Service account | `video-service` | Signs URLs for the API |
| Service account | `video-transcode` | Runs the workflow and receives events |
| Workflow | `video-transcode` | Starts Transcoder jobs |
| Eventarc trigger | `video-uploaded` | Starts the workflow on new objects |

### Bucket and URL signing

Replace `<you@example.com>` with your Google account.

```sh
PROJECT=project-2964aeaf-bfcb-4139-a96
BUCKET=project-2964aeaf-bfcb-4139-a96-videos
REGION=asia-south1
SA=video-service@$PROJECT.iam.gserviceaccount.com

gcloud services enable iamcredentials.googleapis.com

gcloud storage buckets create gs://$BUCKET --project=$PROJECT --location=$REGION \
  --uniform-bucket-level-access --public-access-prevention

gcloud iam service-accounts create video-service --project=$PROJECT

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

On Cloud Run, attach the service account to the service instead of running the
login command.

### CORS

Save this as `cors.json`. Add your real site to `origin` when you deploy.

```json
[{
  "origin": ["http://localhost:8080"],
  "method": ["GET", "HEAD", "PUT"],
  "responseHeader": ["Content-Type", "Range", "x-goog-content-length-range"],
  "maxAgeSeconds": 3600
}]
```

```sh
gcloud storage buckets update gs://$BUCKET --cors-file=cors.json
```

### Transcoding

```sh
PROJECT_NUMBER=$(gcloud projects describe $PROJECT --format='value(projectNumber)')
TRANSCODE_SA=video-transcode@$PROJECT.iam.gserviceaccount.com
TOKEN=$(gcloud auth print-access-token)

gcloud services enable transcoder.googleapis.com workflows.googleapis.com \
  workflowexecutions.googleapis.com eventarc.googleapis.com pubsub.googleapis.com

# Creates the Transcoder and Cloud Storage service agents if they do not exist.
curl -X POST -H "Authorization: Bearer $TOKEN" \
  "https://serviceusage.googleapis.com/v1beta1/projects/$PROJECT/services/transcoder.googleapis.com:generateServiceIdentity"
curl -H "Authorization: Bearer $TOKEN" \
  "https://storage.googleapis.com/storage/v1/projects/$PROJECT/serviceAccount"

# Lets the Transcoder read uploads and write HLS.
gcloud storage buckets add-iam-policy-binding gs://$BUCKET \
  --member=serviceAccount:service-$PROJECT_NUMBER@gcp-sa-transcoder.iam.gserviceaccount.com \
  --role=roles/storage.objectAdmin

# Lets Cloud Storage publish events.
gcloud projects add-iam-policy-binding $PROJECT \
  --member=serviceAccount:service-$PROJECT_NUMBER@gs-project-accounts.iam.gserviceaccount.com \
  --role=roles/pubsub.publisher --condition=None

gcloud iam service-accounts create video-transcode --project=$PROJECT

for role in transcoder.editor eventarc.eventReceiver workflows.invoker logging.logWriter; do
  gcloud projects add-iam-policy-binding $PROJECT \
    --member=serviceAccount:$TRANSCODE_SA --role=roles/$role --condition=None
done

gcloud workflows deploy video-transcode --location=$REGION \
  --source=transcode.workflow.yaml --service-account=$TRANSCODE_SA

gcloud eventarc triggers create video-uploaded --location=$REGION \
  --destination-workflow=video-transcode --destination-workflow-location=$REGION \
  --event-filters=type=google.cloud.storage.object.v1.finalized \
  --event-filters=bucket=$BUCKET --service-account=$TRANSCODE_SA
```

A new service agent can take a few minutes to appear in IAM. If a grant says it
does not exist, wait and run the command again. A new trigger can take up to 2
minutes to become active.

### Cloud CDN

Playback goes through a load balancer with a CDN backend bucket. Adding a signing
key makes the backend reject unsigned requests, so the bucket stays private.

```bash
PROJECT_NUMBER=$(gcloud projects describe $PROJECT --format='value(projectNumber)')

umask 077
head -c 16 /dev/urandom | base64 | tr '+/' '-_' | tr -d '\n' > cdn.key

gcloud compute addresses create video-cdn-ip --global
gcloud compute backend-buckets create video-cdn-backend --gcs-bucket-name=$BUCKET \
  --enable-cdn --cache-mode=FORCE_CACHE_ALL --default-ttl=86400 \
  --signed-url-cache-max-age=86400
gcloud compute backend-buckets add-signed-url-key video-cdn-backend \
  --key-name=video-cdn-key --key-file=cdn.key
gcloud compute url-maps create video-cdn-map --default-backend-bucket=video-cdn-backend
gcloud compute target-http-proxies create video-cdn-proxy --url-map=video-cdn-map
gcloud compute forwarding-rules create video-cdn-fr --global --address=video-cdn-ip \
  --target-http-proxy=video-cdn-proxy --ports=80

# Lets Cloud CDN read objects from the private bucket.
gcloud storage buckets add-iam-policy-binding gs://$BUCKET \
  --member=serviceAccount:service-$PROJECT_NUMBER@cloud-cdn-fill.iam.gserviceaccount.com \
  --role=roles/storage.objectViewer
```

Set `CDN_URL=http://<address>`, `CDN_KEY_NAME=video-cdn-key` and `CDN_KEY` to the
contents of `cdn.key`, then delete `cdn.key`. A new load balancer can take a few
minutes to start serving.

## Play in an external media player

Open the HLS playlist as a network stream in VLC or mpv while the server is
running:

```sh
vlc "http://localhost:8080/videos/13608b29-ecd7-4c55-afd8-2ac86baa9154/hls/manifest.m3u8"
mpv "http://localhost:8080/videos/13608b29-ecd7-4c55-afd8-2ac86baa9154/hls/manifest.m3u8"
```

For another device on the same network, replace `localhost` with this computer's
local IP address.

## Design decisions

| Decision | Reason |
|---|---|
| Direct upload to GCS | The API never handles large files |
| Sign a URL for exact type and size | The URL cannot store a different file |
| Sign with a service account | No long-lived key file is needed |
| Keep the bucket private | Access only comes through signed URLs |
| Derive object paths from the ID | No extra column is needed |
| Serve playlists from the API | Their relative paths must stay on the API, where segments can be signed |
| Redirect each segment | Signing on request means segment links cannot expire during playback |
| Fall back to the original file | Videos can be tried before transcoding finishes |
| Use a workflow to start jobs | It needs no extra Go packages or deployed code |
| Filter by `videos/` in the workflow | The trigger fires for the whole bucket, and the filter stops output from starting new jobs |
| Prefer `hls.js` | It can retry after a network error |

## Limitations

- **No login.** Anyone who can reach the API can upload and play videos.
- **Unfinished uploads stay listed.** Metadata is saved first, so a failed upload
  still appears in the list and `stream` returns `404` for it.
- **Signed playback URLs are shareable.** Anyone with one can use it until it
  expires.
- **No resume for uploads.** A single `PUT` restarts after a failure, and uploads
  are limited to 5 GiB.
- **New uploads only.** Files uploaded before the trigger existed are not
  transcoded.
- **Maximum 720p.** That is the highest quality in `preset/web-hd`.
- **Unused output.** The preset also writes DASH and MP4 files, which take up
  storage.
- **Billing.** Each upload starts a Transcoder job charged by minutes of output
  video, and storage and network use are also billed.
- **Extra workflow runs.** The trigger fires for every Transcoder output file.
  Those runs do nothing and finish as `SUCCEEDED`.
- **API load.** The CDN serves video bytes, but playlists and each segment's
  signed URL still come from the API.
- **CDN uses HTTP.** It has no certificate, so a page served over HTTPS could not
  load video from it.
- **Failed jobs are not reported.** The video keeps playing as the original file.
- **`hls.js` comes from a CDN.** The watch page needs internet access for it.

## Troubleshooting

| Problem | Cause and fix |
|---|---|
| `GCS_BUCKET is required` | Run `set -a; source .env; set +a` |
| Credentials error at startup | Run the `gcloud auth application-default login` command from the setup |
| `500` after changing the database | An old server is still running, so restart it |
| Page changes do not appear | Restart the server, then hard-refresh with `Cmd+Shift+R` |
| Upload fails with a CORS error | Add the page's origin to the bucket CORS rule |
| Video stays in `file` format | The video may be converting, or was uploaded before the trigger existed. Check the job list below |

```sh
gcloud workflows executions list video-transcode --location=asia-south1
curl -H "Authorization: Bearer $(gcloud auth print-access-token)" \
  "https://transcoder.googleapis.com/v1/projects/$PROJECT/locations/asia-south1/jobs"
```

## Current status

**Tested with curl and real GCS:** signed uploads, wrong-size rejection, CORS
rules, the transcoding chain, HLS playlists, segment redirects and original-file
fallback.

**Tested in Chrome:** uploading and playing a video before the `hls.js` fix.

**Not yet confirmed:** playback with the `hls.js` fix, automatic recovery after a
connection loss, and playback in VLC or mpv.
