# The Tomte agent runtime image (cmd/agent-runtime). Behavior lives in
# the mounted agent.yaml, never here — this image is only the program
# that reads it. Build from tomtectl/:
#   docker build -t tomte-agent:0.2.0 .
# For kind: kind load docker-image tomte-agent:0.2.0
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /agent-runtime ./cmd/agent-runtime

# distroless/static carries CA certificates (needed for https
# endpoints) and nothing else — no shell, no package manager.
FROM gcr.io/distroless/static:nonroot
COPY --from=build /agent-runtime /agent-runtime
ENTRYPOINT ["/agent-runtime"]
