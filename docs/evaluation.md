# Evaluation

Whether a model finds its way with the tools, which no unit test shows. Ask these in a new chat of
the [demo](demo.md), as alice, after a change to a tool's name, description or input. A pass is the
answer, by about the tools named; another route that gets there is fine, a guess without a tool
call is not.

The first six only read and stay the same on every fresh demo. The last three change things;
`mise run demo:reset` takes them back.

1. Which project spaces can I see, and who is in Handbook with which role?
   `list_spaces`, `list_space_members`. Handbook, "How we work"; alice manages it, bob views it.
2. What does the onboarding guide in Handbook say about the first day?
   `search` or `list_documents`, `read_section`. Ask for your keys at the front desk.
3. Which documents mention keys?
   `search` by content. `guides/onboarding.md` in Handbook; finding it by a word inside needs Tika.
4. Could bob change the onboarding guide?
   `list_space_members` or `list_permissions`. No, he only views Handbook.
5. Is there a support group I could share with?
   `find_groups`. Yes, Support.
6. What happened in Handbook lately?
   `list_activities`. Admin added alice and bob, then `guides` and `onboarding.md` in it.
7. Write a first-week checklist into my personal space, then let bob edit it.
   `write_file`, `list_share_roles`, `share_item`. A markdown file like `first-week.md`, shared with
   bob as an editor; bob sees it under shared with him.
8. Make an empty spreadsheet budget in my personal space and give me the link to edit it.
   `create_document`, `open_in_app`. `budget.ods`, and a `cloud.localhost/external?app=...` link
   that opens it in Collabora.
9. I deleted the checklist by mistake, bring it back.
   `list_trash`, `restore_from_trash`. After `delete_item` on the checklist, it is back at
   its path.
