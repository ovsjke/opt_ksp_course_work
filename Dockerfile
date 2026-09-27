FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/app ./cmd/app && CGO_ENABLED=0 go build -o /out/migrate ./cmd/migrate && CGO_ENABLED=0 go build -o /out/seed ./cmd/seed && CGO_ENABLED=0 go build -o /out/measure ./cmd/measure && CGO_ENABLED=0 go build -o /out/dbstats ./cmd/dbstats
FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D app
WORKDIR /app
COPY --from=build /out/ /app/
COPY templates /app/templates
COPY static /app/static
USER app
EXPOSE 8080
CMD ["sh", "-c", "./migrate && exec ./app"]
