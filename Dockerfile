FROM golang:1.26-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/finance ./cmd/finance

FROM alpine:3.21
RUN adduser -D -H -u 1000 finance && mkdir /data && chown finance:finance /data
USER finance
COPY --from=build /out/finance /usr/local/bin/finance
ENV FINANCE_DB=/data/finance.db
EXPOSE 8080
ENTRYPOINT ["finance"]
CMD ["-addr", "0.0.0.0:8080"]
