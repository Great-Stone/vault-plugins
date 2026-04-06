FROM gradle:8.10-jdk21 AS build
WORKDIR /app
COPY . .
RUN gradle --no-daemon clean bootJar

FROM eclipse-temurin:21-jre
WORKDIR /app
COPY --from=build /app/build/libs/app.jar /app/app.jar
CMD ["java","-jar","/app/app.jar"]

