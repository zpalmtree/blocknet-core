//! Times the daemon's PoW verify path (`blocknet_pow_hash`), optionally while
//! other threads hash to load memory bandwidth like a busy pool verifier.
//!
//!   pow_bench <hashes> [load_threads]
//!
//! Prints one JSON line: the first hash (arena allocation and page faults
//! included) separately from the steady-state hashes.

use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;
use std::time::Instant;

use blocknet_crypto as _;

extern "C" {
    fn blocknet_pow_hash(header: *const u8, header_len: usize, nonce: u64, output: *mut u8) -> i32;
}

fn pow_hash(header: &[u8], nonce: u64) -> [u8; 32] {
    let mut output = [0u8; 32];
    // Safety: valid header slice and a 32-byte output buffer.
    let rc = unsafe { blocknet_pow_hash(header.as_ptr(), header.len(), nonce, output.as_mut_ptr()) };
    assert_eq!(rc, 0, "pow hash failed");
    output
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let hashes: usize = args.get(1).and_then(|v| v.parse().ok()).unwrap_or(10);
    let load_threads: usize = args.get(2).and_then(|v| v.parse().ok()).unwrap_or(0);
    let header = [0x5au8; 80];

    let stop = Arc::new(AtomicBool::new(false));
    let load_hashes = Arc::new(AtomicU64::new(0));
    let workers: Vec<_> = (0..load_threads)
        .map(|thread| {
            let stop = Arc::clone(&stop);
            let load_hashes = Arc::clone(&load_hashes);
            std::thread::spawn(move || {
                let mut nonce = 1_000_000 * (thread as u64 + 1);
                while !stop.load(Ordering::Relaxed) {
                    pow_hash(&header, nonce);
                    nonce += 1;
                    load_hashes.fetch_add(1, Ordering::Relaxed);
                }
            })
        })
        .collect();
    if load_threads > 0 {
        // Let the load threads allocate and fault in their arenas first.
        while load_hashes.load(Ordering::Relaxed) < load_threads as u64 {
            std::thread::sleep(std::time::Duration::from_millis(50));
        }
    }

    let mut times_ms = Vec::with_capacity(hashes);
    for nonce in 0..hashes as u64 {
        let started = Instant::now();
        pow_hash(&header, nonce);
        times_ms.push(started.elapsed().as_secs_f64() * 1000.0);
    }
    stop.store(true, Ordering::Relaxed);
    for worker in workers {
        worker.join().unwrap();
    }

    let first = times_ms[0];
    let mut steady: Vec<f64> = times_ms[1..].to_vec();
    steady.sort_by(|a, b| a.partial_cmp(b).unwrap());
    let mean = steady.iter().sum::<f64>() / steady.len().max(1) as f64;
    let p50 = steady.get(steady.len() / 2).copied().unwrap_or(f64::NAN);
    println!(
        "{{\"hashes\":{hashes},\"load_threads\":{load_threads},\"first_ms\":{first:.1},\"steady_mean_ms\":{mean:.1},\"steady_p50_ms\":{p50:.1},\"steady_min_ms\":{:.1},\"steady_max_ms\":{:.1}}}",
        steady.first().copied().unwrap_or(f64::NAN),
        steady.last().copied().unwrap_or(f64::NAN)
    );
}
