#!/usr/bin/env bash

# usage : /scripts/mountpoint.sh dw-k8s-fs s3 
#         --role=client 
#         --args='-f -o default_permissions -o allow_other -o fsname=dw-k8s-fs -o fstype=s3 -o user=dingofs -o conf=/dingofs/conf/client.conf /dfs/dw-pv-gamma-uabfer'

g_dingofs_tool="dingo"
g_dingofs_tool_operator="create fs"
g_dingofs_tool_config="config fs"
g_fsname=""
g_fstype="--fstype="
g_quota_capacity="--capacity="
g_quota_inodes="--inodes="
g_entrypoint="/entrypoint.sh"
g_mnt=""
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

function getMountpoint(){
    local long_opts="role:,args:"
    local args=`getopt -o ra --long $long_opts -n "$0" -- "$@"`
    eval set -- "${args}"
    while true; do
      case "$1" in
        -r|--role)
            shift 2
            ;;
        -a|--args)
            g_mnt=$(echo "$2" | awk '{print $NF}')
            g_fsname=$(echo "$2" | awk -F 'fsname=' '{print $2}' | awk '{print $1}')
            shift 2
            ;;
        --) shift; break ;;
        *) echo "Unknown option: $1"; exit 1 ;;
      esac
    done
}


function cleanMountpoint(){

    # Check if mountpoint path is broken (Transport endpoint is not connected)
    if mountpoint -q "${g_mnt}"; then
        echo "Mountpoint ${g_mnt} is mounted properly. begin umount it "
        umount -l "${g_mnt}"
    elif grep -q 'Transport endpoint is not connected' < <(ls "${g_mnt}" 2>&1); then
        echo "Mountpoint ${g_mnt} is in 'Transport endpoint is not connected' state. Forcing umount..."
        fusermount -u "${g_mnt}" || umount -l "${g_mnt}"
    fi

    # Get the MDS address from the client.conf file
    mdsaddr=$(grep 'mdsOpt.rpcRetryOpt.addrs' "${CONFIG_FILE}" | awk -F '=' '{print $2}')
        
    # Get the metric port from the mountpoint list
    mnt_info=$(${g_dingofs_tool} list mountpoint --mdsaddr=${mdsaddr} | grep ${g_mnt} | grep $(hostname))

    # check if mnt_info is empty, skip the following steps
    if [ -z "$mnt_info" ]; then
        echo "current have not mountpoint on $(hostname), skip umount..."
    else
        echo "avoid mountpoint conflict, begin umount mountpoint on $(hostname)..."
        metric_port=$(echo "$mnt_info" | awk -F '[:]' '{print $2}')
        echo "mountpoint ${g_mnt} metric_port is ${metric_port}"
        ${g_dingofs_tool} umount fs --fsname ${g_fsname} --mountpoint $(hostname):${metric_port}:${g_mnt} --mdsaddr=${mdsaddr}
    
        # check above command is successful or not
        if [ $? -ne 0 ]; then
            echo "umount mountpoint failed, exit..."
            exit 1
        fi
    fi

    # check if mountpoint path is transport endpoint is not connected, execute umount 
    
}

updateFuseConfig
getMountpoint "$@" 

ret=$?
if [ $ret -eq 0 ]; then
    cleanMountpoint
    echo "$g_entrypoint $@"
    $g_entrypoint "$@"
    ret=$?
    exit $ret
else
    echo "dingo-fuse configuration update FAILED"
    exit 1
fi
