FROM golang:1.27-trixie AS base

WORKDIR /src

ADD go.* .
RUN go mod download

ADD . .

ARG SHA
ENV CGO_CFLAGS="-O2 -g -DHAVE_STRCHRNUL=1"
RUN go build -ldflags="-X github.com/rueian/pgcapture/cmd.CommitSHA=${SHA}" -x -o pgcapture main.go

FROM gcr.io/distroless/base-debian13

COPY --from=base /src/pgcapture /pgcapture

ENTRYPOINT ["/pgcapture"]