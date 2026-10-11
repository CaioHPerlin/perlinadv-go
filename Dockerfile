FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN export CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH; \
    go build -o /out/api ./cmd/api && \
    go build -o /out/worker ./cmd/worker && \
    go build -o /out/migrate ./cmd/migrate && \
    go build -o /out/admin ./cmd/admin && \
    go build -o /out/research ./cmd/research && \
    go build -o /out/calendar-prepare ./cmd/calendar-prepare && \
    go build -o /out/evaluate ./cmd/evaluate

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/ /app/
COPY migrations/ /app/migrations/
USER 10001:10001
CMD ["/app/api"]
