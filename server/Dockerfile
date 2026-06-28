FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26.4 AS build
WORKDIR /src
COPY server/go.mod server/go.sum ./server/
WORKDIR /src/server
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -o /out/vibe-server ./cmd/server

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /out/vibe-server /app/vibe-server
COPY --from=web /src/web/dist /app/web
EXPOSE 8080
ENTRYPOINT ["/app/vibe-server"]
