# Frontend build.
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# Backend build, with the UI embedded.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/manager ./cmd/manager

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/manager /manager
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/manager"]
