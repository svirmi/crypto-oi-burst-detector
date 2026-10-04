FROM golang:1.26-alpine AS builder

WORKDIR /app

# The glob go.su[m] matches go.sum if it exists and silently skips it if it doesn't. 
# When you eventually add a dependency, no Dockerfile change needed.
COPY go.mod go.su[m] ./  

RUN go mod download
RUN go mod verify

COPY . .

# main.go is at root, so build target is "."
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" \
    -o /symbol-resolver \
    .

FROM gcr.io/distroless/static-debian11:nonroot

COPY --from=builder /symbol-resolver /symbol-resolver

CMD ["/symbol-resolver"]