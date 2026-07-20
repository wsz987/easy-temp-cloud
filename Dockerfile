FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/easy-temp-host .

FROM alpine:3.20

COPY --from=build /out/easy-temp-host /usr/local/bin/easy-temp-host
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/easy-temp-host"]
