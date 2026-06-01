# Multi-stage Dockerfile that produces a k6 image with this extension built in.
FROM golang:1.23-alpine AS build
RUN apk add --no-cache git
WORKDIR /src
COPY . .
RUN go install go.k6.io/xk6/cmd/xk6@latest
RUN xk6 build --with $(go list -m)=. --output /out/k6

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/k6 /usr/bin/k6
ENTRYPOINT ["k6"]
