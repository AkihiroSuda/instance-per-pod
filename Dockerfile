FROM golang:1.13-alpine AS compile
COPY . /go/src/github.com/AkihiroSuda/instance-per-pod
RUN go build -ldflags="-s -w" -o /ipp github.com/AkihiroSuda/instance-per-pod/cmd/ipp

FROM alpine:3.10
COPY --from=compile /ipp /ipp
ENTRYPOINT ["/ipp"]
