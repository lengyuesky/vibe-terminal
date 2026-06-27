use std::collections::VecDeque;

#[derive(Debug, Clone, PartialEq)]
pub struct OutputFrame {
    pub seq: i64,
    pub data: String,
}

#[derive(Debug)]
pub struct OutputBuffer {
    max_frames: usize,
    next_seq: i64,
    frames: VecDeque<OutputFrame>,
}

impl OutputBuffer {
    pub fn new(max_frames: usize) -> Self {
        Self {
            max_frames,
            next_seq: 1,
            frames: VecDeque::new(),
        }
    }

    pub fn push(&mut self, data: String) -> OutputFrame {
        let frame = OutputFrame {
            seq: self.next_seq,
            data,
        };
        self.next_seq += 1;
        self.frames.push_back(frame.clone());
        while self.frames.len() > self.max_frames {
            self.frames.pop_front();
        }
        frame
    }

    pub fn frames_since(&self, seq: i64) -> Vec<OutputFrame> {
        self.frames
            .iter()
            .filter(|frame| frame.seq > seq)
            .cloned()
            .collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn output_buffer_evicts_old_frames() {
        let mut buffer = OutputBuffer::new(2);
        buffer.push("one".to_string());
        buffer.push("two".to_string());
        buffer.push("three".to_string());
        let frames = buffer.frames_since(0);
        assert_eq!(frames.len(), 2);
        assert_eq!(frames[0].seq, 2);
        assert_eq!(frames[1].data, "three");
    }
}
