FROM python:3.12.7-alpine3.20

COPY sim_agent.py /usr/local/bin/anvil-sim-agent
COPY scenarios/ /opt/anvil-sim/scenarios/
RUN chmod 0555 /usr/local/bin/anvil-sim-agent && chmod -R a+rX /opt/anvil-sim

ENV PYTHONUNBUFFERED=1
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/anvil-sim-agent"]
