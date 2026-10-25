FROM quay.io/quarkus/ubi9-quarkus-mandrel-builder-image:jdk-25@sha256:bfea64bfc6e86d4bbb8f301ac227a4da08e5778c8d3b93b1e10ef3c35d16e32c AS build
USER root
WORKDIR /project
COPY --chown=1001:0 . .
USER 1001
RUN ./mvnw -B -ntp package -Dnative -DskipTests -Dquarkus.native.native-image-xmx=4g

FROM quay.io/quarkus/ubi9-quarkus-micro-image:2.0@sha256:aa84cf7f06096050253e740b2b6e679044c268c4db7971483777f1c61a501190
WORKDIR /work
COPY --from=build --chown=1001:0 /project/target/*-runner /work/application
USER 1001
ENTRYPOINT ["/work/application"]
