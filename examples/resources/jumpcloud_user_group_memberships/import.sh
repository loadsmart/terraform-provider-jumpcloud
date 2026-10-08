# Import by user ID. The imported group_ids include every static group the user belongs to
# directly; dynamic groups are skipped. Import reads each of the user's groups once to tell
# them apart, so it takes longer for users in many groups.
terraform import jumpcloud_user_group_memberships.ana 5f1b881dc9e9a9b7e8d6c5a4
