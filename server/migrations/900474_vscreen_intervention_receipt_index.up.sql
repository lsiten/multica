CREATE UNIQUE INDEX CONCURRENTLY runtime_vscreen_intervention_receipt_idx ON runtime_vscreen_intervention (runtime_id, native_epoch, return_receipt_id) WHERE return_receipt_id <> '';
