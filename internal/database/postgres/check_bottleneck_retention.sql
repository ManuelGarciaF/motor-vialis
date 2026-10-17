SELECT
    ($1::timestamptz AT TIME ZONE 'UTC')::date
        < (clock_timestamp() AT TIME ZONE 'UTC')::date - ($2::integer - 1)
