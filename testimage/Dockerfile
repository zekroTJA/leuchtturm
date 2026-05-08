FROM alpine:latest
ARG BUILD_VERSION
RUN NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ") && cat <<EOF > /entrypoint.sh
#!/usr/bin/env sh
echo "version:    ${BUILD_VERSION}"
echo "timestamp:  ${NOW}"
while true; do sleep 1; done
EOF
RUN chmod +x /entrypoint.sh
ENTRYPOINT [ "/entrypoint.sh" ]
