# Build the application in the trusted main project, never download an executable here.
# python3 ops.py prepare-image verifies SHA256, Linux ELF architecture and static linkage.
FROM scratch
COPY --chmod=0555 dist/folio /folio
# An explicitly supplied, hash-verified PEM trust bundle enables outgoing HTTPS.
COPY --chmod=0444 dist/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
USER 65532:65532
WORKDIR /data
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/folio"]
CMD ["serve", "--data", "/data", "--addr", "0.0.0.0:8080"]
