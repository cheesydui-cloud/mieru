FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm install
COPY web/ ./
RUN npm run build

FROM golang:1.22-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.Version=${VERSION}" -o /out/panel ./cmd/panel
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/agent ./cmd/agent

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/panel /app/panel
COPY --from=build /out/agent /app/agent
ENV PANEL_LISTEN=:8080 PANEL_DB=/data/panel.db PANEL_DATA=/data
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/panel"]
