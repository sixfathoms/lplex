FROM golang:1.25 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /lplex ./cmd/lplex

FROM scratch
COPY --from=build /lplex /lplex
ENTRYPOINT ["/lplex"]
