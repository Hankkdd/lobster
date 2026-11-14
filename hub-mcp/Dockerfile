FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -o /hub-mcp .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /hub-mcp /hub-mcp
ENTRYPOINT ["/hub-mcp"]
