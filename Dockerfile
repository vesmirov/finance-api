FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/finance ./cmd/finance

FROM alpine:3.21
RUN adduser -D -H finance
USER finance
COPY --from=build /out/finance /usr/local/bin/finance
EXPOSE 8080
ENTRYPOINT ["finance"]
CMD ["-addr", "0.0.0.0:8080"]
