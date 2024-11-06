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
    local stream_info
    stream_info=$(ffprobe -v quiet -print_format json -show_streams -i "$input_file")
    
    # Try to find English audio stream
    local eng_index
    # First, look for eng language tag
    eng_index=$(echo "$stream_info" | jq -r '.streams[] | select(.codec_type=="audio" and .tags.language=="eng") | .index' 2>/dev/null | head -n 1)
    
    if [ -n "$eng_index" ]; then
        echo "Found English audio stream by language tag: $eng_index"
        echo "$eng_index"
        return
    fi
    
    # Then look for English in title
    eng_index=$(echo "$stream_info" | jq -r '.streams[] | select(.codec_type=="audio" and (.tags.title | ascii_downcase | contains("english"))) | .index' 2>/dev/null | head -n 1)
    
    if [ -n "$eng_index" ]; then
        echo "Found English audio stream by title: $eng_index"
        echo "$eng_index"
        return
    fi
    
    # Finally, just use the first audio stream
    eng_index=$(echo "$stream_info" | jq -r '.streams[] | select(.codec_type=="audio") | .index' 2>/dev/null | head -n 1)
    
    if [ -n "$eng_index" ]; then
        echo "Using first available audio stream: $eng_index"
        echo "$eng_index"
        return
    fi
    
    echo "No audio stream found, using 0"
    echo "0"
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
        -analyzeduration 200M -probesize 200M \
        -i "$input_file" \
        -map 0:v:0 -c:v copy \
        -map "0:a:${audio_stream}" \
        -c:a:0 ac3 \
        -ac:a:0 2 \
        -b:a:0 384k \
        -filter:a:0 "volume=1.5,dynaudnorm=f=150:g=15:p=0.7,loudnorm=I=-16:TP=-1.5:LRA=11" \
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
