FROM golang:1.26.6-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 go build -o /out/migrate ./cmd/migrate && \
    CGO_ENABLED=0 go build -o /out/admin ./cmd/admin && \
    CGO_ENABLED=0 go build -o /out/research ./cmd/research && \
    CGO_ENABLED=0 go build -o /out/calendar-prepare ./cmd/calendar-prepare && \
    CGO_ENABLED=0 go build -o /out/evaluate ./cmd/evaluate

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/ /app/
COPY migrations/ /app/migrations/
USER 10001:10001
CMD ["/app/api"]
