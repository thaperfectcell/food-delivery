-- Инициализация PostgreSQL: отдельная база данных и отдельная роль для каждого
-- микросервиса (паттерн Database per Service). Выполняется один раз при первом
-- запуске контейнера postgres (docker-entrypoint-initdb.d).
-- Сервис имеет доступ только к своей базе; схемы создаёт сам сервис миграциями.

CREATE ROLE auth         LOGIN PASSWORD 'auth';
CREATE ROLE restaurant   LOGIN PASSWORD 'restaurant';
CREATE ROLE orders       LOGIN PASSWORD 'orders';
CREATE ROLE payment      LOGIN PASSWORD 'payment';
CREATE ROLE delivery     LOGIN PASSWORD 'delivery';
CREATE ROLE notification LOGIN PASSWORD 'notification';

CREATE DATABASE auth         OWNER auth;
CREATE DATABASE restaurant   OWNER restaurant;
CREATE DATABASE orders       OWNER orders;
CREATE DATABASE payment      OWNER payment;
CREATE DATABASE delivery     OWNER delivery;
CREATE DATABASE notification OWNER notification;

-- Запрещаем подключение к чужим базам всем, кроме владельца.
REVOKE CONNECT ON DATABASE auth         FROM PUBLIC;
REVOKE CONNECT ON DATABASE restaurant   FROM PUBLIC;
REVOKE CONNECT ON DATABASE orders       FROM PUBLIC;
REVOKE CONNECT ON DATABASE payment      FROM PUBLIC;
REVOKE CONNECT ON DATABASE delivery     FROM PUBLIC;
REVOKE CONNECT ON DATABASE notification FROM PUBLIC;
