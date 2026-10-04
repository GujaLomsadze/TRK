#!/usr/bin/env sh
# Seed a running daemon with a believable fleet. Usage: TRK_PORT=17777 sh scripts/seed-demo.sh
set -eu
U="http://127.0.0.1:${TRK_PORT:-7777}/v1/events"
p() { curl -fsS -o /dev/null -H 'Content-Type: application/json' -d "$1" "$U"; }
hook() { p "{\"source\":\"hook\",\"payload\":{\"session_id\":\"$1\",\"cwd\":\"$2\",\"hook_event_name\":\"$3\"$4}}"; }
cli() { p "{\"source\":\"cli\",\"kind\":\"$2\",\"session_id\":\"$1\",\"session_via\":\"env\",\"payload\":$3}"; }
st() { p "{\"source\":\"statusline\",\"payload\":{\"session_id\":\"$1\",\"model\":{\"display_name\":\"Opus 5.5\"},\"cost\":{\"total_cost_usd\":$2},\"context_window\":{\"used_percentage\":$3,\"context_window_size\":200000,\"total_input_tokens\":$4,\"total_output_tokens\":4200},\"rate_limits\":{\"five_hour\":{\"used_percentage\":23,\"resets_at\":$(( $(date +%s) + 8000 ))},\"seven_day\":{\"used_percentage\":61,\"resets_at\":$(( $(date +%s) + 300000 ))}}}}"; }

hook kafka /src/kafka-consumer SessionStart ''
cli kafka start '{"text":"Refactor Kafka consumer","steps":5}'
cli kafka progress '{"i":3,"n":5}'
cli kafka step '{"text":"Writing tests"}'
for i in 1 2 3 4 5 6; do hook kafka /src/kafka-consumer PostToolUseFailure ",\"tool_name\":\"Bash\",\"tool_use_id\":\"f$i\",\"tool_input\":{\"command\":\"pytest tests/test_consumer.py -x\"}"; done
st kafka 3.12 54 108000

hook api /src/api-gateway SessionStart ''
cli api start '{"text":"Add rate limiting to /v2","steps":4}'
cli api progress '{"i":1,"n":4}'
hook api /src/api-gateway PreToolUse ',"tool_name":"Edit","tool_use_id":"e1","tool_input":{"file_path":"/src/shared/limits.go"}'
hook api /src/api-gateway PermissionRequest ',"tool_name":"Bash","tool_use_id":"b1","tool_input":{"command":"terraform apply -auto-approve"}'
st api 0.84 72 144000

hook docs /src/docs SessionStart ''
cli docs start '{"text":"Update migration guide","steps":3}'
hook docs /src/docs PreToolUse ',"tool_name":"Read","tool_use_id":"r1","tool_input":{"file_path":"/src/shared/limits.go"}'
hook docs /src/docs PostToolUse ',"tool_name":"Read","tool_use_id":"r1","tool_input":{"file_path":"/src/shared/limits.go"}'
cli docs blocked '{"text":"Drop or keep the legacy topic during cutover?"}'
st docs 0.31 18 36000
echo seeded
