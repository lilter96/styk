FROM golang:1.27.3 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/api ./cmd/api && CGO_ENABLED=0 go build -o /out/worker ./cmd/worker && CGO_ENABLED=0 go build -o /out/demo ./cmd/demo && CGO_ENABLED=0 go build -o /out/moysklad-import ./cmd/moysklad-import && CGO_ENABLED=0 go build -o /out/ozon-import ./cmd/ozon-import && CGO_ENABLED=0 go build -o /out/matching ./cmd/matching && CGO_ENABLED=0 go build -o /out/ozon-postings ./cmd/ozon-postings
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ /usr/local/bin/
CMD ["/usr/local/bin/api"]
