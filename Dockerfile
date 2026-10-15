# Этап сборки: компилируем статический бинарник.
FROM golang:1.27-alpine AS build
WORKDIR /src

# Сначала только зависимости — этот слой кешируется, пока не меняется go.mod/go.sum.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# Итоговый образ: только бинарник, без Go и shell, запуск не от root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
