FROM alpine:3.21
RUN apk add --no-cache python3 ca-certificates \
    && adduser -D -u 65532 -g 65532 -H nonroot
COPY confer.py /usr/local/bin/anvil-council-llm
RUN chmod 0555 /usr/local/bin/anvil-council-llm
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/anvil-council-llm"]
