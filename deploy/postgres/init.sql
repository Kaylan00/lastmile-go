-- Roda uma vez, quando o volume do Postgres é criado pelo docker-compose.
-- Cada serviço tem o seu banco: um serviço não lê as tabelas do outro.
CREATE DATABASE dispatch;
CREATE DATABASE tracking;
