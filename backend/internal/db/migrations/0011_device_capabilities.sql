-- NULL means an older agent that predates feature reporting.
ALTER TABLE devices ADD COLUMN capabilities jsonb;
ALTER TABLE devices ADD CONSTRAINT devices_capabilities_object
    CHECK (capabilities IS NULL OR jsonb_typeof(capabilities) = 'object');
