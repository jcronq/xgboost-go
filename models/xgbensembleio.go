package models

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/dmitryikh/leaves/transformation"
)

type xgboostJSON struct {
	NodeID                int            `json:"nodeid,omitempty"`
	SplitFeatureID        string         `json:"split,omitempty"`
	SplitFeatureThreshold float64        `json:"split_condition,omitempty"`
	YesID                 int            `json:"yes,omitempty"`
	NoID                  int            `json:"no,omitempty"`
	MissingID             int            `json:"missing,omitempty"`
	LeafValue             float64        `json:"leaf,omitempty"`
	Children              []*xgboostJSON `json:"children,omitempty"`
}

func loadFeatureMap(filePath string) (map[string]int, error) {
	featureFile, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer featureFile.Close()

	read := bufio.NewReader(featureFile)
	featureMap := make(map[string]int, 0)
	for {
		// feature map format: feature_index feature_name feature_type
		line, err := read.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		tk := strings.Split(line, " ")
		if len(tk) != 3 {
			return nil, fmt.Errorf("wrong feature map format")
		}
		featIdx, err := strconv.Atoi(tk[0])
		if err != nil {
			return nil, err
		}
		if _, ok := featureMap[tk[1]]; ok {
			return nil, fmt.Errorf("duplicate feature name")
		}
		featureMap[tk[1]] = featIdx
	}
	return featureMap, nil
}

func convertFeatToIdx(featureMap map[string]int, feature string) (int, error) {
	if featureMap != nil {
		if idx, ok := featureMap[feature]; !ok {
			return 0, fmt.Errorf("cannot find feature %s in feature map", feature)
		} else {
			return idx, nil
		}
	}

	// if no feature map use the default feature name which are: f0, f1, f2, ...
	feature = feature[1:]
	idx, err := strconv.Atoi(feature)
	if err != nil {
		return 0, err
	}
	return idx, nil
}

func buildTree(xgbTreeJSON *xgboostJSON, maxDepth int, featureMap map[string]int) (*xgbTree, int, error) {
	stack := make([]*xgboostJSON, 0)
	fMap := make(map[int]struct{})
	t := &xgbTree{}
	stack = append(stack, xgbTreeJSON)
	var node *xgbNode
	var maxNumNodes int
	if maxDepth != 0 {
		maxNumNodes = int(math.Pow(2, float64(maxDepth+1)) - 1)
		t.nodes = make([]*xgbNode, maxNumNodes)
	}
	for len(stack) > 0 {
		stackData := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if stackData.Children == nil {
			// leaf node.
			node = &xgbNode{
				NodeID:     stackData.NodeID,
				Flags:      isLeaf,
				LeafValues: stackData.LeafValue,
			}
		} else {
			featIdx, err := convertFeatToIdx(featureMap, xgbTreeJSON.SplitFeatureID)
			if _, ok := fMap[featIdx]; !ok {
				fMap[featIdx] = struct{}{}
			}
			if err != nil {
				return nil, 0, err
			}
			node = &xgbNode{
				NodeID:    stackData.NodeID,
				Threshold: stackData.SplitFeatureThreshold,
				No:        stackData.NoID,
				Yes:       stackData.YesID,
				Missing:   stackData.MissingID,
				Feature:   featIdx,
			}
			for _, c := range stackData.Children {
				stack = append(stack, c)
			}
		}
		if maxNumNodes > 0 {
			if node.NodeID >= maxNumNodes {
				log.Fatalf("wrong tree max depth %d, please check your model again for the correct parameter",
					maxDepth)
			}
			t.nodes[node.NodeID] = node
		} else {
			// do not know the depth beforehand just append.
			t.nodes = append(t.nodes, node)
		}
	}
	if maxDepth == 0 {
		sort.SliceStable(t.nodes, func(i, j int) bool {
			return t.nodes[i].NodeID < t.nodes[j].NodeID
		})
	}

	return t, len(fMap), nil
}

// LoadXGBoostFromJSON loads xgboost model from json file.
func LoadXGBoostFromJSON(modelPath,
	featuresMapPath string,
	numClasses int,
	maxDepth int,
	loadTransformation bool) (*EnsembleBase, error) {
	modelFile, err := os.Open(modelPath)
	if err != nil {
		return nil, err
	}
	defer modelFile.Close()

	var xgbEnsembleJSON []*xgboostJSON

	dec := json.NewDecoder(modelFile)
	err = dec.Decode(&xgbEnsembleJSON)
	if err != nil {
		return nil, err
	}
	var featMap map[string]int
	if len(featuresMapPath) != 0 {
		featMap, err = loadFeatureMap(featuresMapPath)
		if err != nil {
			return nil, err
		}
	}

	// TODO: Add num class check here, totalTrees % numClasses == 0
	if numClasses <= 0 {
		return nil, fmt.Errorf("num class cannot be 0 or smaller: %d", numClasses)
	}

	if maxDepth < 0 {
		return nil, fmt.Errorf("max depth cannot be smaller than 0: %d", maxDepth)
	}

	e := &xgbEnsemble{name: "xgboost", numClasses: numClasses}
	nTrees := len(xgbEnsembleJSON)
	if nTrees == 0 {
		return nil, fmt.Errorf("no trees in file")
	} else if nTrees%e.numClasses != 0 {
		return nil, fmt.Errorf("wrong number of trees %d for number of class %d", nTrees, e.numClasses)
	}

	e.Trees = make([]*xgbTree, 0, nTrees)
	maxFeat := 0
	for i := 0; i < nTrees; i++ {
		tree, numFeat, err := buildTree(xgbEnsembleJSON[i], maxDepth, featMap)
		if err != nil {
			return nil, fmt.Errorf("error while reading %d tree: %s", i, err.Error())
		}
		e.Trees = append(e.Trees, tree)
		if numFeat > maxFeat {
			maxFeat = numFeat
		}
	}
	e.numFeat = maxFeat

	// TODO: Change transformation function.
	return &EnsembleBase{Transform: &transformation.TransformRaw{NumOutputGroups: e.numClasses}}, nil
}