FROM golang AS build
WORKDIR /go/src/github.com/mildred/nomadspace
COPY . .
ENV CGO_ENABLED 0
ENV GO111MODULE on
RUN go get ./...
RUN go install . ./plugins/... ./cmd/...

FROM alpine
RUN apk add --no-cache dnsmasq
COPY --from=build /go/bin/nomadspace /bin/nomadspace
COPY --from=build /go/bin/ns /bin/ns
CMD /bin/nomadspace
