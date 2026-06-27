FROM golang:1.22 AS build
WORKDIR /src
COPY server/go.mod ./server/
WORKDIR /src/server
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -o /out/vibe-server ./cmd/server

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /out/vibe-server /app/vibe-server
EXPOSE 8080
ENTRYPOINT ["/app/vibe-server"]
