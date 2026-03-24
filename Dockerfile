FROM scratch
ARG TARGETARCH
COPY linux/${TARGETARCH}/lplex-server /lplex-server
COPY linux/${TARGETARCH}/lplex /lplex
ENTRYPOINT ["/lplex-server"]
