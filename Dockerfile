# FROM dingodatabase/dingofs:latest
# FROM harbor.zetyun.cn/dingofs/dingofs:6ebcf21b470b1038bb7047cb91544e667b55fc2b
FROM harbor.zetyun.cn/dingofs/dingofs:v3.0.16
RUN sed -i "s diskCache.diskCacheType=0 diskCache.diskCacheType=2 g" /dingofs/conf/client.conf
ADD bin/dingofs-csi-driver /usr/bin/dingofs-csi-driver
# ADD https://github.com/krallin/tini/releases/download/v0.19.0/tini-amd64 /bin/tini
COPY bin/tini-amd64 /bin/tini
RUN chmod +x /bin/tini
ENTRYPOINT [ "/bin/tini", "--", "/usr/bin/dingofs-csi-driver"]
