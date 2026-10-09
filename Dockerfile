FROM golang:alpine AS build
WORKDIR /build
COPY cmd/ cmd/
COPY pkg/ pkg/
COPY go.mod .
COPY go.sum .
RUN go build -o leuchtturm cmd/leuchtturm/main.go

FROM scratch
ENV DOCKER_CONFIG=/root/.docker
COPY --from=build /build/leuchtturm /leuchtturm
ENTRYPOINT [ "/leuchtturm" ]
