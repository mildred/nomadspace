FROM golang AS build
WORKDIR /go/src/github.com/mildred/nomadspace
COPY . .
ENV CGO_ENABLED 0
RUN go get .
RUN go install .

FROM alpine
COPY --from=build /go/bin/nomadspace /bin/nomadspace
CMD /bin/nomadspace
