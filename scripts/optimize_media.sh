#!/bin/bash

# Configuration
THREADS=$(nproc)  # Get number of CPU threads
MEM_LIMIT="6G"    # Memory limit per FFmpeg process
NICE_LEVEL=10     # Nice level for CPU priority
IO_CLASS="realtime"  # I/O class (none, realtime, best-effort, idle)
IO_PRIORITY=5     # I/O priority (0-7, 7 being lowest)

# Function to sanitize filename for temporary files
sanitize_filename() {
    local filename="$1"
    # Remove special characters and replace spaces with underscores
    echo "$filename" | tr -cd '[:alnum:]._-' | tr ' ' '_'
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
    echo "Checking video codec..."
    codec=$(ffprobe -v error -select_streams v:0 -show_entries stream=codec_name -of default=nw=1:nk=1 "$input_file" 2>&1)
    
    # Create temp directory if it doesn't exist
    mkdir -p "$temp_dir"
    
    # Set process priority
    renice -n "$NICE_LEVEL" -p $$ > /dev/null
    ionice -c "$IO_CLASS" -n "$IO_PRIORITY" -p $$

    # Calculate optimal thread count based on file size
    file_size=$(stat -c %s "$input_file")
    if [ "$file_size" -gt 10737418240 ]; then  # 10GB
        thread_count=$((THREADS - 1))
    else
        thread_count=$((THREADS / 2))
    fi


    # Create sanitized temporary filename
    temp_id="$(date +%s%N)"
    # temp_output="${temp_dir}/temp_${temp_id}.${extension}"
    temp_output="${temp_dir}/temp_${temp_id}.mp4"
    progress_file="${temp_dir}/progress_${temp_id}.txt"
    
    echo "Processing file: $input_file"
    echo "Temporary output: $temp_output"
    echo "Progress file: $progress_file"
    echo "Current Video codec: $codec"
    
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

    # Only process audio if codec is HEVC
    if [ "$codec" = "hevc" ]; then
        echo "Video already in HEVC format, processing audio only..."
        ffmpeg -loglevel debug -i "$input_file" -map 0:v:0 -map 0:a:m:language:eng -metadata:s:a title="2.1 Optimized" -metadata:s:a language=eng -c:v copy -c:a ac3 -ac 2 -b:a 384k -af "dynaudnorm=f=500:g=15:p=0.95:r=0.5,volume=1.2" -f mp4 -movflags +faststart "$temp_output"
    else
        echo "Converting video to HEVC..."
        ffmpeg -loglevel debug -i "$input_file" -map 0:v:0 -map 0:a:m:language:eng -metadata:s:a title="2.1 Optimized" -metadata:s:a language=eng -c:v libx265 -preset medium -crf 26 -c:a ac3 -ac 2 -b:a 384k -af "dynaudnorm=f=500:g=15:p=0.95:r=0.5,volume=1.2" -f mp4 -movflags +faststart "$temp_output"
    fi    


    # Check FFmpeg exit status
    if [ $? -eq 0 ] && [ -f "$temp_output" ]; then
        mv "$temp_output" "$output_file"
        echo "Successfully processed: $input_file"
        ffprobe -v error -select_streams a:0 -show_entries stream=channel_layout,channels -of default=noprint_wrappers=1  "$output_file"
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
