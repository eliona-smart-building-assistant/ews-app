CREATE TABLE IF NOT EXISTS ews.unified_booking
(
    id                         bigserial PRIMARY KEY,
    exchange_uid               text UNIQUE,
    exchange_organizer_mailbox text,
    booking_id                 int UNIQUE
);

CREATE TABLE IF NOT EXISTS ews.room_booking
(
    id                  bigserial PRIMARY KEY,
    unified_booking_id  bigserial NOT NULL REFERENCES ews.unified_booking(id) ON DELETE CASCADE,
    exchange_id         text UNIQUE
);

INSERT INTO ews.unified_booking (exchange_uid, exchange_organizer_mailbox, booking_id)
SELECT DISTINCT exchange_uid, exchange_mailbox, booking_id
FROM ews.booking;

INSERT INTO ews.room_booking (unified_booking_id, exchange_id)
SELECT ub.id, b.exchange_id
FROM ews.booking b
JOIN ews.unified_booking ub ON b.exchange_uid = ub.exchange_uid;

DROP TABLE IF EXISTS ews.booking;
