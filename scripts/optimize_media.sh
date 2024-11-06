#!/bin/bash

# Configuration
THREADS=$(nproc)  # Get number of CPU threads
MEM_LIMIT="4G"    # Memory limit per FFmpeg process
NICE_LEVEL=10     # Nice level for CPU priority
IO_CLASS="best-effort"
IO_PRIORITY=7     # I/O priority (0-7, 7 being lowest)

# Function to sanitize filename for temporary files
sanitize_filename() {
    local filename="$1"
    # Remove special characters and replace spaces with underscores
    echo "$filename" | tr -cd '[:alnum:]._-' | tr ' ' '_'
}

# Function to find English audio stream
find_eng_audio_stream() {
    local input_file="$1"
    
    # Get stream information with enhanced verbosity
    local stream_info
    stream_info=$(ffprobe -v error -show_streams -print_format json "$input_file")
    
    # Debug: Print full stream information
    echo "DEBUG: Full stream information:" >&2
    echo "$stream_info" >&2
    
    # Detect audio streams with explicit checks
    local audio_streams=$(echo "$stream_info" | grep -o '"index": *[0-9]*.*"codec_type": *"audio"' | grep -o '"index": *[0-9]*' | grep -o '[0-9]*')
    
    echo "DEBUG: Found audio stream indices: $audio_streams" >&2
    
    # Detailed stream information extraction
    local stream_details=$(echo "$stream_info" | grep -E '"index"|"codec_name"|"channels"|"language"|"title"')
    
    echo "DEBUG: Audio Stream Details:" >&2
    echo "$stream_details" >&2
    
    # Prioritize stream selection
    local selected_stream=""
    
    # First, look for stream with English language and 5.1 channels
    for stream in $audio_streams; do
        local lang_check=$(echo "$stream_info" | grep -E "\"index\": *$stream.*\"language\": *\"eng\"")
        local channel_check=$(echo "$stream_info" | grep -E "\"index\": *$stream.*\"channels\": *6")
        local title_check=$(echo "$stream_info" | grep -E "\"index\": *$stream.*\"title\": *\".*[Ee]nglish\"")
        
        if [ -n "$lang_check" ] && [ -n "$channel_check" ]; then
            selected_stream=$stream
            break
        fi
    done
    
    # If no English 5.1 stream, look for any English stream
    if [ -z "$selected_stream" ]; then
        for stream in $audio_streams; do
            local lang_check=$(echo "$stream_info" | grep -E "\"index\": *$stream.*\"language\": *\"eng\"")
            local title_check=$(echo "$stream_info" | grep -E "\"index\": *$stream.*\"title\": *\".*[Ee]nglish\"")
            
            if [ -n "$lang_check" ] || [ -n "$title_check" ]; then
                selected_stream=$stream
                break
            fi
        done
    fi
    
    # If no English stream, prefer 5.1 or stereo streams
    if [ -z "$selected_stream" ]; then
        for stream in $audio_streams; do
            local channel_check=$(echo "$stream_info" | grep -E "\"index\": *$stream.*\"channels\": *\(6\|2\)")
            if [ -n "$channel_check" ]; then
                selected_stream=$stream
                break
            fi
        done
    fi
    
    # Final fallback: first audio stream
    if [ -z "$selected_stream" ]; then
        selected_stream=$(echo "$audio_streams" | head -n 1)
    fi
    
    # Final check
    if [ -z "$selected_stream" ]; then
        echo "Error: No audio streams found" >&2
        return 1
    fi
    
    echo "DEBUG: Selected audio stream index: $selected_stream" >&2
    echo "$selected_stream"
    return 0
}

# Function to process a single file
process_file() {
    input_file="$1"
    filename=$(basename "$input_file")
    dirname=$(dirname "$input_file")
    extension="${filename##*.}"
    basename="${filename%.*}"
    output_file="${dirname}/${basename}_optimized.${extension}"
    temp_dir="/tmp/ffmpeg_processing"
    
    # Create temp directory if it doesn't exist
    mkdir -p "$temp_dir"
    
    # Set process priority
    renice -n "$NICE_LEVEL" -p $$ > /dev/null
    ionice -c "$IO_CLASS" -n "$IO_PRIORITY" -p $$

    # Calculate optimal thread count based on file size
    file_size=$(stat -c %s "$input_file")
    if [ "$file_size" -gt 10737418240 ]; then  # 10GB
        thread_count=$THREADS
    else
        thread_count=$((THREADS / 2))
    fi

    # Find English audio stream
    audio_stream=$(find_eng_audio_stream "$input_file")
    stream_status=$?
    
    if [ $stream_status -ne 0 ]; then
        echo "Error: No suitable audio stream found in $input_file"
        # List all streams for debugging
        ffprobe -v error -show_streams -print_format json "$input_file"
        exit 1
    fi
    
    echo "Using audio stream index: $audio_stream"

    # Create sanitized temporary filename
    temp_id="$(date +%s%N)"
    temp_output="${temp_dir}/temp_${temp_id}.${extension}"
    progress_file="${temp_dir}/progress_${temp_id}.txt"
    
    echo "Processing file: $input_file"
    echo "Temporary output: $temp_output"
    echo "Progress file: $progress_file"
    
    # Function to cleanup on exit
    cleanup() {
        local exit_code=$?
        echo "Cleaning up..."
        # Kill any remaining ffmpeg processes
        pkill -P $$
        # Remove temporary files
        rm -f "$temp_output" "$progress_file"
        exit $exit_code
    }
    trap cleanup EXIT INT TERM

    # Get duration for progress calculation
    duration=$(ffprobe -v quiet -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 "$input_file")
    echo "total_duration=$duration" > "$progress_file"
    
    # Process with FFmpeg using optimized settings
    ffmpeg -nostdin -y \
        -analyzeduration 100M -probesize 100M \
        -i "$input_file" \
        -map 0:v:0 -c:v copy \
        -map "0:a:${audio_stream}" \
        -c:a ac3 \
        -ac 2 \
        -b:a 384k \
        -filter:a "volume=1.5,dynaudnorm=f=150:g=15:p=0.7,loudnorm=I=-16:TP=-1.5:LRA=11" \
        -metadata:s:a:0 title="2.1 Optimized" \
        -metadata:s:a:0 language=eng \
        -movflags +faststart \
        -max_muxing_queue_size 1024 \
        -threads "$thread_count" \
        -progress "$progress_file" \
        "$temp_output" || exit 1

    # Move the file to final destination
    if [ -f "$temp_output" ]; then
        mv "$temp_output" "$output_file"
        echo "Successfully processed: $input_file"
        echo "Output saved to: $output_file"
        exit 0
    else
        echo "Failed to process: $input_file"
        exit 1
    fi
}

# Main script
if [ -z "$1" ]; then
    echo "Usage: $0 <input_file>"
    exit 1
fi

input_file="$1"

if [ ! -f "$input_file" ]; then
    echo "Error: Input file does not exist"
    exit 1
fi

# Process the file
process_file "$input_file"
