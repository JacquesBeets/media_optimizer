#!/bin/bash

# Configuration
THREADS=$(nproc)  # Get number of CPU threads
MEM_LIMIT="4G"    # Memory limit per FFmpeg process
NICE_LEVEL=10     # Nice level for CPU priority
IO_CLASS="best-effort"
IO_PRIORITY=7     # I/O priority (0-7, 7 being lowest)

# Function to sanitize filename
sanitize_filename() {
    local filename="$1"
    # First, escape the filename for sed
    filename=$(echo "$filename" | sed 's/[][\*\^\&\$\?\*(){}#|]/\\&/g')
    # Then remove/replace special characters
    filename=$(echo "$filename" | tr -d '{}[]()' | tr ' ' '_')
    echo "$filename"
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

    # Get English audio stream if available
    audio_stream=0
    audio_info=$(ffprobe -v quiet -print_format json -show_streams "$input_file")
    eng_stream=$(echo "$audio_info" | jq -r '.streams[] | select(.codec_type=="audio" and .tags.language=="eng") | .index' 2>/dev/null)
    if [ ! -z "$eng_stream" ]; then
        audio_stream=$eng_stream
    fi

    # Create sanitized temporary filename
    temp_basename=$(sanitize_filename "${basename}")
    temp_output="${temp_dir}/temp_${temp_basename}.${extension}"
    
    # Create progress file for monitoring
    progress_file="${temp_dir}/progress_${temp_basename}.txt"
    
    echo "Processing file: $input_file"
    echo "Temporary output: $temp_output"
    echo "Progress file: $progress_file"
    
    # Process with FFmpeg using optimized settings
    ffmpeg -analyzeduration 100M -probesize 100M \
        -i "$input_file" \
        -map 0:v:0 -c:v copy \
        -map "0:a:${audio_stream}" \
        -c:a ac3 \
        -ac 2 \
        -b:a 384k \
        -af "volume=1.5,dynaudnorm=f=150:g=15:p=0.7,loudnorm=I=-16:TP=-1.5:LRA=11" \
        -metadata:s:a:0 title="2.1 Optimized" \
        -metadata:s:a:0 language=eng \
        -movflags +faststart \
        -threads "$thread_count" \
        -y \
        -nostdin \
        -progress "$progress_file" \
        "$temp_output"

    # Check if FFmpeg was successful
    if [ $? -eq 0 ]; then
        # Move completed file to final destination
        mv "$temp_output" "$output_file"
        rm -f "$progress_file"
        echo "Successfully processed: $input_file"
        echo "Output saved to: $output_file"
        exit 0
    else
        rm -f "$temp_output" "$progress_file"
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
