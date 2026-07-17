FROM busybox:1.37.0

COPY entrypoint.sh /usr/local/bin/anvil-demo-agent
RUN chmod 0555 /usr/local/bin/anvil-demo-agent

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/anvil-demo-agent"]
