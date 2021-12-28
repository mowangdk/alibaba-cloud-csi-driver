FROM centos:7.9.2009
LABEL maintainers="Alibaba Cloud Authors"
LABEL description="Alibaba Cloud CSI DiskPlugin"

RUN yum install -y e4fsprogs

COPY nsenter /
COPY diskplugin.csi.alibabacloud.com /bin/diskplugin.csi.alibabacloud.com
RUN chmod +x /bin/diskplugin.csi.alibabacloud.com
RUN chmod 755 /nsenter

ENTRYPOINT ["/bin/diskplugin.csi.alibabacloud.com"]
