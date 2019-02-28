FROM golang AS build
WORKDIR /go/src/github.com/mildred/nomadspace
COPY . .
RUN go get .
RUN go install .

FROM alpine
COPY --from=build /go/bin/nomadspace /bin/nomadspace
CMD /bin/nomadspace
