FROM scratch
ARG TARGETARCH
COPY linux/${TARGETARCH}/lplex /lplex
ENTRYPOINT ["/lplex"]
