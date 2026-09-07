FROM centos:7
LABEL maintainers="Alibaba Cloud Authors"
LABEL description="Alibaba Cloud CSI DiskPlugin"

RUN yum install -y e4fsprogs

COPY diskplugin.csi.alibabacloud.com /bin/diskplugin.csi.alibabacloud.com
RUN chmod +x /bin/diskplugin.csi.alibabacloud.com

ENTRYPOINT ["/bin/diskplugin.csi.alibabacloud.com"]
