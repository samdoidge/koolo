package step

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/hectorgimenez/d2go/pkg/data"
	"github.com/hectorgimenez/d2go/pkg/data/object"
	"github.com/hectorgimenez/d2go/pkg/data/skill"
	"github.com/hectorgimenez/d2go/pkg/data/stat"
	"github.com/hectorgimenez/koolo/internal/context"
	"github.com/hectorgimenez/koolo/internal/game"
	"github.com/hectorgimenez/koolo/internal/utils"
)

const DistanceToFinishMoving = 4

var (
	ErrMonstersInPath        = errors.New("monsters detected in movement path")
	stepLastMonsterCheck     = time.Time{}
	stepMonsterCheckInterval = 500 * time.Millisecond
)

type MoveOpts struct {
	distanceOverride *int
}

type MoveOption func(*MoveOpts)

// WithDistanceToFinish overrides the default DistanceToFinishMoving
func WithDistanceToFinish(distance int) MoveOption {
	return func(opts *MoveOpts) {
		opts.distanceOverride = &distance
	}
}

func MoveTo(dest data.Position, options ...MoveOption) error {
	// Initialize options
	opts := &MoveOpts{}

	// Apply any provided options
	for _, o := range options {
		o(opts)
	}

	minDistanceToFinishMoving := DistanceToFinishMoving
	if opts.distanceOverride != nil {
		minDistanceToFinishMoving = *opts.distanceOverride
	}

	ctx := context.Get()
	ctx.SetLastStep("MoveTo")

	timeout := time.Second * 30
	idleThreshold := time.Second * 3
	idleStartTime := time.Time{}
	openedDoors := make(map[object.Name]data.Position)

	var walkDuration time.Duration
	// Shorter walkDuration for fluid segment movement outside town.
	if !ctx.Data.AreaData.Area.IsTown() {
		walkDuration = utils.RandomDurationMs(475, 525)
	} else {
		walkDuration = utils.RandomDurationMs(600, 1000)
	}

	startedAt := time.Now()
	lastRun := time.Time{}
	previousPosition := data.Position{}
	previousDistance := 0

	for {
		// Pause the execution if the priority is not the same as the execution priority
		ctx.PauseIfNotPriority()
		// is needed to prevent bot teleporting in circle when it reached destination (lower end cpu) cost is minimal.
		ctx.RefreshGameData()

		// Check for monsters in path - only perform this check periodically to reduce CPU usage
		if !ctx.Data.AreaData.Area.IsTown() && !ctx.Data.CanTeleport() && time.Since(stepLastMonsterCheck) > stepMonsterCheckInterval {
			stepLastMonsterCheck = time.Now()

			monsterFound := false
			clearPathDist := ctx.CharacterCfg.Character.ClearPathDist

			for _, m := range ctx.Data.Monsters.Enemies() {
				// Skip dead monsters
				if m.Stats[stat.Life] <= 0 {
					continue
				}

				// Fast distance calculation for early exit
				distanceToMonster := ctx.PathFinder.DistanceFromMe(m.Position)
				if distanceToMonster <= clearPathDist {
					monsterFound = true
					break
				}
			}

			if monsterFound {
				return ErrMonstersInPath
			}
		}

		// If we can't teleport, check for doors and destructible objects
		if !ctx.Data.CanTeleport() {
			obstacleHandled := handleObstaclesInPath(dest, openedDoors)
			if obstacleHandled {
				continue
			}

			// Add some delay between clicks to let the character move to destination
			if time.Since(lastRun) < walkDuration {
				time.Sleep(walkDuration - time.Since(lastRun))
				continue
			}
		} else {
			// We skip the movement if we can teleport and the last movement time was less than the player cast duration
			if time.Since(lastRun) < ctx.Data.PlayerCastDuration() {
				time.Sleep(ctx.Data.PlayerCastDuration() - time.Since(lastRun))
				continue
			}
		}

		// Check for idle state
		if ctx.Data.PlayerUnit.Position == previousPosition {
			if idleStartTime.IsZero() {
				idleStartTime = time.Now()
			} else if time.Since(idleStartTime) > idleThreshold {
				// Perform anti-idle action
				ctx.Logger.Debug("Anti-idle triggered")
				ctx.PathFinder.RandomMovement()
				idleStartTime = time.Time{} // Reset idle timer
				continue
			}
		} else {
			idleStartTime = time.Time{} // Reset idle timer if position changed
			previousPosition = ctx.Data.PlayerUnit.Position
		}

		// Press the Teleport keybinding if it's available, otherwise use vigor (if available)
		if ctx.Data.CanTeleport() {
			if ctx.Data.PlayerUnit.RightSkill != skill.Teleport {
				ctx.HID.PressKeyBinding(ctx.Data.KeyBindings.MustKBForSkill(skill.Teleport))
			}
		} else if kb, found := ctx.Data.KeyBindings.KeyBindingForSkill(skill.Vigor); found {
			if ctx.Data.PlayerUnit.RightSkill != skill.Vigor {
				ctx.HID.PressKeyBinding(kb)
			}
		}

		path, distance, found := ctx.PathFinder.GetPath(dest)
		if !found {
			if ctx.PathFinder.DistanceFromMe(dest) < minDistanceToFinishMoving+5 {
				return nil
			}

			return errors.New("path could not be calculated. Current area: [" + ctx.Data.PlayerUnit.Area.Area().Name + "]. Trying to path to Destination: [" + fmt.Sprintf("%d,%d", dest.X, dest.Y) + "]")
		}
		if distance <= minDistanceToFinishMoving || len(path) <= minDistanceToFinishMoving || len(path) == 0 {
			return nil
		}

		// Exit on timeout
		if timeout > 0 && time.Since(startedAt) > timeout {
			return nil
		}

		lastRun = time.Now()

		// This is a workaround to avoid the character to get stuck in the same position when the hitbox of the destination is too big
		if distance < 20 && math.Abs(float64(previousDistance-distance)) < DistanceToFinishMoving {
			minDistanceToFinishMoving += DistanceToFinishMoving
		} else if opts.distanceOverride != nil {
			minDistanceToFinishMoving = *opts.distanceOverride
		} else {
			minDistanceToFinishMoving = DistanceToFinishMoving
		}

		previousPosition = ctx.Data.PlayerUnit.Position
		previousDistance = distance
		ctx.PathFinder.MoveThroughPath(path, walkDuration)
	}
}
func handleObstaclesInPath(dest data.Position, openedDoors map[object.Name]data.Position) bool {
    ctx := context.Get()

    // Get current path to destination
    path, _, found := ctx.PathFinder.GetPath(ctx.Data.PlayerUnit.Position, dest)
    if !found {
        return false
    }

	// Check doors first
	for _, o := range ctx.Data.Objects {
		if o.IsDoor() && o.Selectable && openedDoors[o.Name] != o.Position {
			// Verify door is actually in the current path
			for _, pathPos := range path {
				if ctx.PathFinder.DistanceFromPoint(pathPos, o.Position) < 3 {
					ctx.Logger.Debug("Door detected in path, opening...")
					openedDoors[o.Name] = o.Position

					err := InteractObject(o, func() bool {
						obj, found := ctx.Data.Objects.FindByID(o.ID)
						return found && !obj.Selectable
					})

					if err == nil {
						// Refresh path data after door interaction
						ctx.PathFinder.RefreshMapData()
						utils.Sleep(300) // Allow door animation
						return true
					}
					break
				}
			}
		}
	}

	// Check destructibles using path data
	for _, o := range ctx.Data.Objects {
		if o.Name == object.Barrel && ctx.PathFinder.DistanceFromMe(o.Position) < 3 {
			for _, pathPos := range path {
				if ctx.PathFinder.DistanceFromPoint(pathPos, o.Position) < 2 {
					ctx.Logger.Debug("Clearing path obstruction...")
					InteractObject(o, func() bool {
						x, y := ctx.PathFinder.GameCoordsToScreenCords(o.Position.X, o.Position.Y)
						ctx.HID.Click(game.LeftButton, x, y)
						return true
					})
					utils.Sleep(100)
					return true
				}
			}
		}
	}

	return false
}
