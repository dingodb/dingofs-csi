# FROM dingodatabase/dingofs:latest
FROM harbor.zetyun.cn/dingofs/dingofs:latest
RUN sed -i "s diskCache.diskCacheType=0 diskCache.diskCacheType=2 g" /dingofs/conf/client.conf
ADD bin/dingofs-csi-driver /usr/bin/dingofs-csi-driver
# ADD https://github.com/krallin/tini/releases/download/v0.19.0/tini-amd64 /bin/tini
COPY bin/tini-amd64 /bin/tini
RUN chmod +x /bin/tini
ENTRYPOINT [ "/bin/tini", "--", "/usr/bin/dingofs-csi-driver"]
