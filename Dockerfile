FROM scratch
ARG TARGETARCH
COPY linux/${TARGETARCH}/lplex-server /lplex-server
ENTRYPOINT ["/lplex-server"]
