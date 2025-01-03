#!/usr/bin/env bash

g_dingofs_tool="dingo"
g_dingofs_tool_operator="create fs"
g_dingofs_tool_config="config fs"
g_fsname="--fsname="
g_fstype="--fstype="
g_quota_capacity="--capacity="
g_quota_inodes="--inodes="
g_entrypoint="/entrypoint.sh"
# Path to the client.conf file
CONFIG_FILE="/dingofs/conf/client.conf"

function updateFuseConfig() {
    # Read the configuration file line by line
    while IFS= read -r line; do
        # Trim leading and trailing whitespace
        line="${line#"${line%%[![:space:]]*}"}"
        line="${line%"${line##*[![:space:]]}"}"

        # Skip empty lines, lines without '=', and annotation lines starting with '#'
        [[ -z "$line" || "$line" != *=* || "$line" =~ ^# ]] && continue

        # Split key and value, removing surrounding whitespace
        key=$(echo "${line%%=*}" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')
        value=$(echo "${line#*=}" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')

        # Convert the key to an environment variable format (replace dots with underscores)
        env_key="${key//./_}"

        # Check if the corresponding environment variable exists
        if [[ -n "${!env_key}" ]]; then
            # Use sed to replace the entire line, removing spaces around '='
            sed -i "s|^\s*$key\s*=.*|$key=${!env_key}|" "$CONFIG_FILE"
            echo "Updated $key to ${!env_key}"
        fi
    done < "$CONFIG_FILE"

    echo "Configuration update completed."
}

updateFuseConfig

ret=$?
if [ $ret -eq 0 ]; then
    echo "$g_entrypoint $@"
    $g_entrypoint "$@"
    ret=$?
    exit $ret
else
    echo "dingo-fuse configuration update FAILED"
    exit 1
fi
