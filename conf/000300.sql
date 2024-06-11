BEGIN;

-- Check if the migration has already been applied by checking for the existence of the new tables
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'booking_group') THEN

        -- Step 1: Create new tables as per the new schema
        CREATE TABLE IF NOT EXISTS ews.booking_group
        (
            id                         bigserial PRIMARY KEY,
            exchange_uid               text UNIQUE,
            exchange_organizer_mailbox text,
            eliona_group_id            int UNIQUE
        );

        CREATE TABLE IF NOT EXISTS ews.booking_occurrence
        (
            id                      bigserial PRIMARY KEY,
            booking_group_id        bigserial NOT NULL REFERENCES ews.booking_group(id) ON DELETE CASCADE,
            exchange_instance_index int NOT NULL,
            eliona_booking_id       int UNIQUE,
            UNIQUE (booking_group_id, exchange_instance_index)
        );

        CREATE TABLE IF NOT EXISTS ews.room_booking_new
        (
            id                    bigserial PRIMARY KEY,
            booking_occurrence_id bigserial NOT NULL REFERENCES ews.booking_occurrence(id) ON DELETE CASCADE,
            exchange_id           text UNIQUE
        );

        -- Step 2: Migrate data from the original tables to the new schema
        -- Insert into booking_group
        INSERT INTO ews.booking_group (id, exchange_uid, exchange_organizer_mailbox, eliona_group_id)
        SELECT ub.id, ub.exchange_uid, ub.exchange_organizer_mailbox, ub.booking_id
        FROM ews.unified_booking ub
        LEFT JOIN ews.booking_group bg ON ub.id = bg.id
        WHERE bg.id IS NULL;

        -- Insert into booking_occurrence
        INSERT INTO ews.booking_occurrence (booking_group_id, exchange_instance_index, eliona_booking_id)
        SELECT ub.id, 0, ub.booking_id
        FROM ews.unified_booking ub
        LEFT JOIN ews.booking_occurrence bo ON ub.id = bo.booking_group_id
        WHERE bo.id IS NULL;

        -- Insert into room_booking_new
        INSERT INTO ews.room_booking_new (booking_occurrence_id, exchange_id)
        SELECT bo.id, rb.exchange_id
        FROM ews.booking_occurrence bo
        INNER JOIN ews.room_booking rb ON rb.unified_booking_id = bo.booking_group_id
        LEFT JOIN ews.room_booking_new rbn ON rb.exchange_id = rbn.exchange_id
        WHERE rbn.id IS NULL;

        -- Step 3: Drop old tables and rename the new table to the original name
        DROP TABLE IF EXISTS ews.room_booking;
        ALTER TABLE ews.room_booking_new RENAME TO room_booking;

        -- Step 4: Drop old unified_booking table
        DROP TABLE IF EXISTS ews.unified_booking;

    END IF;
END $$;

COMMIT;