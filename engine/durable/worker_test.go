package durable

import (
	"reflect"
	"testing"
)

func TestWorkerShards(t *testing.T) {
	tests := []struct {
		name    string
		workers int
		shards  int
		want    map[int][]int
	}{
		{
			name:    "EqualWorkersAndShards",
			workers: 4,
			shards:  4,
			want: map[int][]int{
				0: {0},
				1: {1},
				2: {2},
				3: {3},
			},
		},
		{
			name:    "MoreShardsThanWorkers",
			workers: 2,
			shards:  4,
			want: map[int][]int{
				0: {0, 2},
				1: {1, 3},
			},
		},
		{
			name:    "14Workers16Shards",
			workers: 14,
			shards:  16,
			want: map[int][]int{
				0:  {0, 14},
				1:  {1, 15},
				2:  {2},
				13: {13},
			},
		},
		{
			name:    "MoreWorkersThanShards",
			workers: 4,
			shards:  2,
			want: map[int][]int{
				0: {0},
				1: {1},
				2: {0},
				3: {1},
			},
		},
		{
			name:    "SingleWorkerMultipleShards",
			workers: 1,
			shards:  4,
			want: map[int][]int{
				0: {0, 1, 2, 3},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := &Engine{opt: Options{Workers: tc.workers, Shards: tc.shards}}
			// Ensure all shards are covered across workers
			covered := make(map[int]bool)
			for w := range tc.workers {
				got := e.workerShards(w)
				for _, s := range got {
					covered[s] = true
				}
				if wantShards, ok := tc.want[w]; ok {
					if !reflect.DeepEqual(got, wantShards) {
						t.Errorf("worker %d shards = %v, want %v", w, got, wantShards)
					}
				}
			}
			if len(covered) != tc.shards {
				t.Errorf("covered shards count = %d, want %d", len(covered), tc.shards)
			}
		})
	}
}
