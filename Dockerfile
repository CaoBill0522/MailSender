FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /mailsender ./cmd/mailsender

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Shanghai MS_DATA_DIR=/data MS_LISTEN=:8080
COPY --from=build /mailsender /usr/local/bin/mailsender
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["mailsender"]
