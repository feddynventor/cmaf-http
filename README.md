# cmaf-http

cmaf-http is a small, experimental toolkit that ingests fragmented MP4 data from named pipes, advertises available fragments to interested clients, and serves playable fragment bundles over plain HTTP. 

It is designed as a testing and experimentation origin for workflows that combine event-driven discovery (SSE) with on-demand HTTP retrieval.

This ingester was built mainly for experimenting with different CDN providers, starting from a barebone and then extensible ingesting logic.

What it does (high level)
- Ingests raw MP4 fragment atoms fed into OS pipes.
- Groups atoms into playable fragments and keeps them available in memory-backed storage for fast serving.
- Broadcasts lightweight JSON updates about newly available fragments via Server‑Sent Events (SSE).
- Provides simple HTTP endpoints to fetch initialization data and consecutive fragment bundles for playback or analysis.
- Provides a small JSON manifest endpoint for a snapshot of current state.

Main features (short)
- SSE-based advertisement of fragment availability — subscribe and receive updates when new fragments arrive.
- Named-pipe ingestion — any producer that writes MP4 atoms to a configured pipe can feed the origin.
- Lightweight fragment assembly and in-memory materialization for quick streaming.
- Simple, easy-to-use HTTP endpoints for init segments, fragment bundles and a diagnostic manifest.

High-level pipeline (brief)
1. Producer writes MP4 atoms to a named pipe
   - Typical atoms: moov (initialization), moof (fragment metadata), mdat (media data).
2. Ingest & parse
   - The server reads atom headers and payloads, recognizes moov/moof/mdat, and extracts basic metadata (timestamps, sequence).
3. Assemble fragments
   - Matching moof + mdat are combined and materialized into fast, in-memory files so the server can stream them without copying large buffers.
4. Advertise availability (SSE)
   - When a new fragment window is ready, the server sends a small JSON update over the SSE feed to all subscribed clients.
5. Serve on demand (HTTP)
   - Clients request an init segment or a fragment bundle via HTTP; the server responds with the concatenated data and a few simple headers that describe presentation time and bundle size.

Pipeline (text diagram)
Producer/encoder -> named pipe -> cmaf-http (ingest/parser) -> fragments stored in memory -> SSE events advertise new fragments -> HTTP client requests init/segment -> cmaf-http streams fragment bundle

Primary HTTP endpoints (examples)
- SSE events feed
  - GET {root}/events
  - Returns a text/event-stream. Each SSE `data:` contains a JSON payload describing the latest fragment window.
- Init / representation root
  - GET {root}/{representation}/
  - Returns the stored initialization atom (init segment) for that representation.
- Fragment bundle
  - GET {root}/{representation}/{index}
  - Requests a playable bundle starting at the keyframe identified by index. The response is the concatenated fragment bytes plus a few headers describing PTS and number of combined fragments.
- Manifest snapshot
  - GET {root}/
  - Returns a JSON manifest describing configured representations, head position and keyframe points (for diagnostics).

Quick usage sketch (non-technical)
- Start the server with your config (it reads a TOML config that lists representations and named pipe paths).
- Point your producer/encoder to write MP4 atoms into the configured named pipe for a representation.
- Subscribe to the SSE endpoint to receive timely JSON updates when new fragments arrive.
- Use the HTTP endpoints to download the init segment and subsequent fragment bundles for playback or validation.

Simple example client interactions
- Subscribe to events (SSE):
  - curl -N http://server:port/{root}/events
- Get init segment:
  - curl http://server:port/{root}/{representation}/ -o init.mp4
- Get a fragment bundle starting at index 123:
  - curl http://server:port/{root}/{representation}/123 -o segment-123.m4s

Operational notes (concise)
- The server materializes fragments using fast, in-memory file primitives where available (optimized for Linux environments).
- The design focuses on experimentation: small JSON diagnostics, SSE for realtime discovery, and simple concatenated fragment delivery over HTTP.
- Intended as a test/prototyping origin, not as a full production DASH/HLS origin.

Configuration & build (high level)
- The program reads a TOML config file that declares representations and their named pipe paths.
- Build/run (example):
  - go build -o cmaf-http ./...
  - ./cmaf-http config.toml

Where to look in the code (guide)
- main.go — program bootstrap, config loading, pipe wiring and manifest endpoint
- input_stream.go — ingest/parsing, fragment assembly, fragment lifecycle
- mp4_parser.go — lightweight MP4 atom parsing helpers used at ingest time
- sse.go — SSE broadcaster and HTTP handler
- server.go — HTTP serving of init and fragment bundles

Contributing & license
- Contributions that improve clarity, examples, tests, and small utilities for feeding or consuming the server are welcome.
- See LICENSE in the repository root for license terms.

If you want, I can:
- Insert a one-page Quickstart with exact commands and a sample config.toml,
- Add a tiny SSE client example (HTML or curl+jq) and a small producer script that demonstrates feeding a named pipe.
Tell me which and I’ll add it to the README.  
