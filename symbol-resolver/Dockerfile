FROM golang:1.26.3-alpine AS golang

WORKDIR /app
COPY . .

RUN go mod download
RUN go mod verify

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /symbol-resolver .

FROM gcr.io/distroless/static-debian11

COPY --from=golang /symbol-resolver .

# EXPOSE 8080

CMD ["/symbol-resolver"]