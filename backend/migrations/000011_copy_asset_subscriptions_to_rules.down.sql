-- Removes every rule, including rules users created after the upgrade:
-- once a copied rule's baseline is cleared it cannot be told apart from a
-- user-created one. Rolling back 000011 on its own is not supported; it is
-- only meant to run right before 000010's down, which drops the table (#31).
DELETE FROM asset_rules;
