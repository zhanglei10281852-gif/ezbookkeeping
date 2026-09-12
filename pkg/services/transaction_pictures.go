package services

import (
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"xorm.io/xorm"

	"github.com/mayswind/ezbookkeeping/pkg/core"
	"github.com/mayswind/ezbookkeeping/pkg/datastore"
	"github.com/mayswind/ezbookkeeping/pkg/errs"
	"github.com/mayswind/ezbookkeeping/pkg/log"
	"github.com/mayswind/ezbookkeeping/pkg/models"
	"github.com/mayswind/ezbookkeeping/pkg/storage"
	"github.com/mayswind/ezbookkeeping/pkg/utils"
	"github.com/mayswind/ezbookkeeping/pkg/uuid"
)

// transactionPictureCleanupMinObjectAge is the minimum age of an object that is allowed to be reclaimed as an orphan.
// It is used to ensure that an object being uploaded by another in-flight request can never be deleted.
const transactionPictureCleanupMinObjectAge = 24 * time.Hour

// transactionPictureCleanupBatchSize is the batch size used when the cleanup job processes database records
const transactionPictureCleanupBatchSize = 200

// TransactionPictureService represents transaction picture service
type TransactionPictureService struct {
	ServiceUsingDB
	ServiceUsingUuid
	ServiceUsingStorage
}

// Initialize a transaction picture service singleton instance
var (
	TransactionPictures = &TransactionPictureService{
		ServiceUsingDB: ServiceUsingDB{
			container: datastore.Container,
		},
		ServiceUsingUuid: ServiceUsingUuid{
			container: uuid.Container,
		},
		ServiceUsingStorage: ServiceUsingStorage{
			container: storage.Container,
		},
	}
)

// GetTotalTransactionPicturesCountByUid returns total transaction pictures count of user
func (s *TransactionPictureService) GetTotalTransactionPicturesCountByUid(c core.Context, uid int64) (int64, error) {
	if uid <= 0 {
		return 0, errs.ErrUserIdInvalid
	}

	count, err := s.UserDataDB(uid).NewSession(c).Where("uid=? AND deleted=?", uid, false).Count(&models.TransactionPictureInfo{})

	return count, err
}

// GetPictureInfoByPictureId returns a transaction picture info model according to transaction picture id
func (s *TransactionPictureService) GetPictureInfoByPictureId(c core.Context, uid int64, pictureId int64) (*models.TransactionPictureInfo, error) {
	if uid <= 0 {
		return nil, errs.ErrUserIdInvalid
	}

	if pictureId <= 0 {
		return nil, errs.ErrTransactionPictureIdInvalid
	}

	pictureInfo := &models.TransactionPictureInfo{}
	has, err := s.UserDataDB(uid).NewSession(c).ID(pictureId).Where("uid=? AND deleted=?", uid, false).Get(pictureInfo)

	if err != nil {
		return nil, err
	} else if !has {
		return nil, errs.ErrTransactionPictureNotFound
	}

	return pictureInfo, nil
}

// GetNewPictureInfosByPictureIds returns new transaction picture info models according to transaction picture ids
func (s *TransactionPictureService) GetNewPictureInfosByPictureIds(c core.Context, uid int64, pictureIds []int64) ([]*models.TransactionPictureInfo, error) {
	if uid <= 0 {
		return nil, errs.ErrUserIdInvalid
	}

	if pictureIds == nil {
		return nil, errs.ErrTransactionPictureIdInvalid
	}

	var pictureInfos []*models.TransactionPictureInfo
	err := s.UserDataDB(uid).NewSession(c).Where("uid=? AND deleted=? AND transaction_id=?", uid, false, models.TransactionPictureNewPictureTransactionId).In("picture_id", pictureIds).OrderBy("picture_id asc").Find(&pictureInfos)

	if err != nil {
		return nil, err
	}

	return pictureInfos, nil
}

// GetPictureInfosByTransactionId returns transaction picture info models according to transaction id
func (s *TransactionPictureService) GetPictureInfosByTransactionId(c core.Context, uid int64, transactionId int64) ([]*models.TransactionPictureInfo, error) {
	if uid <= 0 {
		return nil, errs.ErrUserIdInvalid
	}

	if transactionId <= 0 {
		return nil, errs.ErrTransactionIdInvalid
	}

	var pictureInfos []*models.TransactionPictureInfo
	err := s.UserDataDB(uid).NewSession(c).Where("uid=? AND deleted=? AND transaction_id=?", uid, false, transactionId).OrderBy("picture_id asc").Find(&pictureInfos)

	if err != nil {
		return nil, err
	}

	return pictureInfos, nil
}

// GetPictureInfosByTransactionIds returns transaction picture info models according to transaction ids
func (s *TransactionPictureService) GetPictureInfosByTransactionIds(c core.Context, uid int64, transactionIds []int64) (map[int64][]*models.TransactionPictureInfo, error) {
	if uid <= 0 {
		return nil, errs.ErrUserIdInvalid
	}

	if transactionIds == nil {
		return nil, errs.ErrTransactionIdInvalid
	}

	var pictureInfos []*models.TransactionPictureInfo
	err := s.UserDataDB(uid).NewSession(c).Where("uid=? AND deleted=?", uid, false).In("transaction_id", transactionIds).OrderBy("picture_id asc").Find(&pictureInfos)

	if err != nil {
		return nil, err
	}

	pictureInfoMap := s.GetPictureInfoListMapByList(pictureInfos)
	return pictureInfoMap, err
}

// GetAllPictureInfosOfAllTransactions returns all transaction picture info models
func (s *TransactionPictureService) GetAllPictureInfosOfAllTransactions(c core.Context, uid int64) (map[int64][]*models.TransactionPictureInfo, error) {
	if uid <= 0 {
		return nil, errs.ErrUserIdInvalid
	}

	var pictureInfos []*models.TransactionPictureInfo
	err := s.UserDataDB(uid).NewSession(c).Where("uid=? AND deleted=?", uid, false).OrderBy("picture_id asc").Find(&pictureInfos)

	if err != nil {
		return nil, err
	}

	pictureInfoMap := s.GetPictureInfoListMapByList(pictureInfos)
	return pictureInfoMap, err
}

// GetPictureByPictureId returns the transaction picture data according to transaction picture id
func (s *TransactionPictureService) GetPictureByPictureId(c core.Context, uid int64, pictureId int64, fileExtension string) ([]byte, error) {
	if uid <= 0 {
		return nil, errs.ErrUserIdInvalid
	}

	if pictureId <= 0 {
		return nil, errs.ErrTransactionPictureIdInvalid
	}

	pictureInfo := &models.TransactionPictureInfo{}
	has, err := s.UserDataDB(uid).NewSession(c).ID(pictureId).Where("uid=? AND deleted=?", uid, false).Get(pictureInfo)

	if err != nil {
		return nil, err
	} else if !has {
		return nil, errs.ErrTransactionPictureNotFound
	}

	if pictureInfo.PictureExtension == "" {
		return nil, errs.ErrTransactionPictureNotFound
	}

	if pictureInfo.PictureExtension != fileExtension {
		return nil, errs.ErrTransactionPictureExtensionInvalid
	}

	// The picture record has been committed, so the object is allowed to be read.
	// If the object is still in the staging area (for example the process restarted before publishing it), try to recover it first.
	pictureData, err := s.readTransactionPictureDataWithRecovery(c, pictureInfo.Uid, pictureInfo.PictureId, pictureInfo.PictureExtension)

	if err != nil {
		return nil, err
	}

	return pictureData, nil
}

// UploadPicture uploads the transaction picture for specified user
func (s *TransactionPictureService) UploadPicture(c core.Context, pictureInfo *models.TransactionPictureInfo, pictureFile multipart.File) error {
	if pictureInfo.Uid <= 0 {
		return errs.ErrUserIdInvalid
	}

	defer pictureFile.Close()

	pictureInfo.PictureId = s.GenerateUuid(uuid.UUID_TYPE_USER)

	if pictureInfo.PictureId < 1 {
		return errs.ErrSystemIsBusy
	}

	pictureInfo.TransactionId = models.TransactionPictureNewPictureTransactionId
	pictureInfo.Deleted = false
	pictureInfo.CreatedUnixTime = time.Now().Unix()
	pictureInfo.UpdatedUnixTime = time.Now().Unix()

	// 1. Save the object to the staging area first. The object can never be read before its database record is committed.
	err := s.SaveStagingTransactionPicture(c, pictureInfo.Uid, pictureInfo.PictureId, pictureFile, pictureInfo.PictureExtension)

	if err != nil {
		return err
	}

	// 2. Insert the picture record in a database transaction
	insertErr := s.UserDataDB(pictureInfo.Uid).DoTransaction(c, func(sess *xorm.Session) error {
		_, err := sess.Insert(pictureInfo)
		return err
	})

	if insertErr != nil {
		// 3a. The record cannot be committed, so rollback the staged object.
		// Only this upload's unique staging path is touched, an object referenced by a transaction can never be deleted here.
		// If the deletion fails temporarily, the staging object is left as an orphan and will be safely reclaimed by the cleanup maintenance job.
		if deleteErr := s.DeleteStagingTransactionPicture(c, pictureInfo.Uid, pictureInfo.PictureId, pictureInfo.PictureExtension); deleteErr != nil {
			log.Warnf(c, "[transaction_pictures.UploadPicture] failed to rollback staging transaction picture \"uid:%d,id:%d\" after database insert failed, because %s, the orphan object will be cleaned up by the cleanup job", pictureInfo.Uid, pictureInfo.PictureId, deleteErr.Error())
		}

		return insertErr
	}

	// 3b. The record has been committed successfully, publish the object to the final path.
	// If the publish fails (for example network failure or process restart), the committed record and the staging object are still retained,
	// the object will be recovered automatically when it is read or by the cleanup maintenance job, and the same picture id can continue to be used.
	if err = s.MoveStagingTransactionPictureToFinal(c, pictureInfo.Uid, pictureInfo.PictureId, pictureInfo.PictureExtension); err != nil {
		log.Errorf(c, "[transaction_pictures.UploadPicture] failed to publish transaction picture \"uid:%d,id:%d\" after the record has been committed, because %s, the object will be recovered automatically", pictureInfo.Uid, pictureInfo.PictureId, err.Error())
	}

	return nil
}

// RemoveUnusedTransactionPicture removes the unused transaction picture of specified user
func (s *TransactionPictureService) RemoveUnusedTransactionPicture(c core.Context, uid int64, pictureId int64) error {
	if uid <= 0 {
		return errs.ErrUserIdInvalid
	}

	if pictureId <= 0 {
		return errs.ErrTransactionPictureIdInvalid
	}

	now := time.Now().Unix()

	updateModel := &models.TransactionPictureInfo{
		Deleted:         true,
		DeletedUnixTime: now,
	}

	return s.UserDataDB(uid).DoTransaction(c, func(sess *xorm.Session) error {
		deletedRows, err := sess.ID(pictureId).Cols("deleted", "deleted_unix_time").Where("uid=? AND deleted=? AND transaction_id=?", uid, false, models.TransactionPictureNewPictureTransactionId).Update(updateModel)

		if err != nil {
			return err
		} else if deletedRows < 1 {
			return errs.ErrTransactionPictureNotFound
		}

		return err
	})
}

// CleanupTransactionPictures recovers pending transaction picture objects and safely removes orphaned objects from the object storage
func (s *TransactionPictureService) CleanupTransactionPictures(c core.Context) error {
	if !s.TransactionPictureStorageReady() {
		return nil
	}

	cutoffTime := time.Now().Add(-transactionPictureCleanupMinObjectAge)
	cutoffUnixTime := cutoffTime.Unix()

	recoveredCount := 0
	removedStagingObjectCount := 0
	removedDeletedPictureCount := 0
	removedOrphanObjectCount := 0
	failedCount := 0

	// Phase 1: Reclaim the objects of the records which have been soft-deleted for longer than the retention time
	removedDeleted, failedDeleted, err := s.cleanupDeletedTransactionPictures(c, cutoffUnixTime)
	removedDeletedPictureCount += removedDeleted
	failedCount += failedDeleted

	if err != nil {
		log.Errorf(c, "[transaction_pictures.CleanupTransactionPictures] failed to cleanup deleted transaction pictures, because %s", err.Error())
	}

	// Phase 2: Reconcile the objects in the staging area
	recovered, removedStaging, failedStaging, err := s.cleanupStagingTransactionPictureObjects(c, cutoffTime)
	recoveredCount += recovered
	removedStagingObjectCount += removedStaging
	failedCount += failedStaging

	if err != nil {
		log.Errorf(c, "[transaction_pictures.CleanupTransactionPictures] failed to cleanup staging transaction picture objects, because %s", err.Error())
	}

	// Phase 3: Reclaim the orphaned objects in the final area (including orphaned objects left by the old upload flow)
	removedOrphan, failedOrphan, err := s.cleanupOrphanedTransactionPictureObjects(c, cutoffTime)
	removedOrphanObjectCount += removedOrphan
	failedCount += failedOrphan

	if err != nil {
		log.Errorf(c, "[transaction_pictures.CleanupTransactionPictures] failed to cleanup orphaned transaction picture objects, because %s", err.Error())
	}

	log.Infof(c, "[transaction_pictures.CleanupTransactionPictures] transaction picture cleanup finished, recovered %d pending objects, removed %d staging objects, removed %d deleted pictures and %d orphaned objects, %d objects failed", recoveredCount, removedStagingObjectCount, removedDeletedPictureCount, removedOrphanObjectCount, failedCount)

	return nil
}

// GetPictureInfoMapByList returns a transaction picture info list map by a list
func (s *TransactionPictureService) GetPictureInfoMapByList(pictureInfos []*models.TransactionPictureInfo) map[int64]*models.TransactionPictureInfo {
	pictureInfoMap := make(map[int64]*models.TransactionPictureInfo)

	for i := 0; i < len(pictureInfos); i++ {
		pictureInfo := pictureInfos[i]
		pictureInfoMap[pictureInfo.PictureId] = pictureInfo
	}

	return pictureInfoMap
}

// GetPictureInfoListMapByList returns a transaction picture info list map by a list
func (s *TransactionPictureService) GetPictureInfoListMapByList(pictureInfos []*models.TransactionPictureInfo) map[int64][]*models.TransactionPictureInfo {
	pictureInfoMap := make(map[int64][]*models.TransactionPictureInfo)

	for i := 0; i < len(pictureInfos); i++ {
		pictureInfo := pictureInfos[i]
		pictureInfos, _ := pictureInfoMap[pictureInfo.TransactionId]
		pictureInfoMap[pictureInfo.TransactionId] = append(pictureInfos, pictureInfo)
	}

	return pictureInfoMap
}

// GetTransactionPictureIds returns transaction picture ids list
func (s *TransactionPictureService) GetTransactionPictureIds(pictureInfos []*models.TransactionPictureInfo) []int64 {
	pictureIds := make([]int64, len(pictureInfos))

	for i := 0; i < len(pictureInfos); i++ {
		pictureIds[i] = pictureInfos[i].PictureId
	}

	return pictureIds
}

func (s *TransactionPictureService) readTransactionPictureDataWithRecovery(c core.Context, uid int64, pictureId int64, fileExtension string) ([]byte, error) {
	pictureData, err := s.readTransactionPictureDataOnce(c, uid, pictureId, fileExtension)

	if err == nil {
		return pictureData, nil
	}

	// The object is not at the final path, try to recover the pending object from the staging area
	if s.tryRecoverPendingTransactionPicture(c, uid, pictureId, fileExtension) {
		pictureData, err = s.readTransactionPictureDataOnce(c, uid, pictureId, fileExtension)
	}

	if os.IsNotExist(err) {
		return nil, errs.ErrTransactionPictureNoExists
	}

	if err != nil {
		return nil, err
	}

	return pictureData, nil
}

func (s *TransactionPictureService) readTransactionPictureDataOnce(c core.Context, uid int64, pictureId int64, fileExtension string) ([]byte, error) {
	pictureFile, err := s.ReadTransactionPicture(c, uid, pictureId, fileExtension)

	if err != nil {
		return nil, err
	}

	defer pictureFile.Close()

	return io.ReadAll(pictureFile)
}

func (s *TransactionPictureService) tryRecoverPendingTransactionPicture(c core.Context, uid int64, pictureId int64, fileExtension string) bool {
	finalExists, err := s.ExistsTransactionPicture(c, uid, pictureId, fileExtension)

	if err != nil || finalExists {
		return false
	}

	stagingExists, err := s.ExistsStagingTransactionPicture(c, uid, pictureId, fileExtension)

	if err != nil || !stagingExists {
		return false
	}

	err = s.MoveStagingTransactionPictureToFinal(c, uid, pictureId, fileExtension)

	if err != nil {
		log.Warnf(c, "[transaction_pictures.tryRecoverPendingTransactionPicture] failed to recover pending transaction picture \"uid:%d,id:%d\", because %s", uid, pictureId, err.Error())
		return false
	}

	log.Infof(c, "[transaction_pictures.tryRecoverPendingTransactionPicture] pending transaction picture \"uid:%d,id:%d\" has been recovered", uid, pictureId)
	return true
}

func (s *TransactionPictureService) cleanupDeletedTransactionPictures(c core.Context, cutoffUnixTime int64) (int, int, error) {
	removedCount := 0
	failedCount := 0

	for i := 0; i < s.UserDataDBCount(); i++ {
		for {
			var pictureInfos []*models.TransactionPictureInfo
			err := s.UserDataDBByIndex(i).NewSession(c).
				Where("deleted=? AND deleted_unix_time>0 AND deleted_unix_time<=?", true, cutoffUnixTime).
				OrderBy("picture_id asc").
				Limit(transactionPictureCleanupBatchSize).
				Find(&pictureInfos)

			if err != nil {
				return removedCount, failedCount, err
			}

			if len(pictureInfos) < 1 {
				break
			}

			for j := 0; j < len(pictureInfos); j++ {
				pictureInfo := pictureInfos[j]

				if err := s.deleteTransactionPictureObjectsAndRecord(c, pictureInfo); err != nil {
					failedCount++
					log.Warnf(c, "[transaction_pictures.cleanupDeletedTransactionPictures] failed to delete objects of deleted transaction picture \"uid:%d,id:%d\", because %s", pictureInfo.Uid, pictureInfo.PictureId, err.Error())
				} else {
					removedCount++
				}
			}

			if len(pictureInfos) < transactionPictureCleanupBatchSize {
				break
			}
		}
	}

	return removedCount, failedCount, nil
}

func (s *TransactionPictureService) cleanupStagingTransactionPictureObjects(c core.Context, cutoffTime time.Time) (int, int, int, error) {
	recoveredCount := 0
	removedCount := 0
	failedCount := 0

	stagingObjects, err := s.ListTransactionPictureObjects(c, transactionPictureStagingPathPrefix)

	if err != nil {
		return recoveredCount, removedCount, failedCount, err
	}

	parsedObjects, unparsedObjects := groupTransactionPictureObjects(stagingObjects, true)

	for uid, objects := range parsedObjects {
		pictureInfoMap, err := s.getPictureInfoMapByPictureIdsAnyState(c, uid, getTransactionPictureIdsFromObjectInfos(objects))

		if err != nil {
			failedCount += len(objects)
			log.Warnf(c, "[transaction_pictures.cleanupStagingTransactionPictureObjects] failed to get transaction picture records for user \"uid:%d\", because %s", uid, err.Error())
			continue
		}

		for i := 0; i < len(objects); i++ {
			object := objects[i]
			pictureInfo, exists := pictureInfoMap[object.PictureId]

			if exists && !pictureInfo.Deleted {
				finalPath := s.getTransactionPicturePath(uid, object.PictureId, pictureInfo.PictureExtension)
				stagingPath := s.getStagingTransactionPicturePath(uid, object.PictureId, object.FileExtension)

				finalExists, err := s.storageContainer().ExistsTransactionPicture(c, finalPath)

				if err != nil {
					failedCount++
					log.Warnf(c, "[transaction_pictures.cleanupStagingTransactionPictureObjects] failed to check final transaction picture \"uid:%d,id:%d\", because %s", uid, object.PictureId, err.Error())
					continue
				}

				if finalExists {
					// The object has been published, the staging object is redundant
					if err := s.deleteTransactionPictureObjectByPath(c, stagingPath); err != nil {
						failedCount++
						log.Warnf(c, "[transaction_pictures.cleanupStagingTransactionPictureObjects] failed to delete redundant staging transaction picture \"uid:%d,id:%d\", because %s", uid, object.PictureId, err.Error())
					} else {
						removedCount++
					}
				} else {
					// The record has been committed but the object has not been published yet, recover it
					if err := s.storageContainer().MoveTransactionPicture(c, stagingPath, finalPath); err != nil {
						failedCount++
						log.Warnf(c, "[transaction_pictures.cleanupStagingTransactionPictureObjects] failed to recover pending transaction picture \"uid:%d,id:%d\", because %s", uid, object.PictureId, err.Error())
					} else {
						recoveredCount++
					}
				}
			} else if exists && pictureInfo.Deleted && pictureInfo.DeletedUnixTime > 0 && pictureInfo.DeletedUnixTime <= cutoffTime.Unix() {
				// The record has been soft-deleted beyond the retention time, delete the object together with the record
				if err := s.deleteTransactionPictureObjectsAndRecord(c, pictureInfo); err != nil {
					failedCount++
					log.Warnf(c, "[transaction_pictures.cleanupStagingTransactionPictureObjects] failed to delete objects of deleted transaction picture \"uid:%d,id:%d\", because %s", uid, object.PictureId, err.Error())
				} else {
					removedCount++
				}
			} else if !exists {
				// There is no record for the staging object. It is either an in-flight upload or an orphan left by a failed insert/crash.
				// Only delete it after it is older than the minimum age, so an in-flight upload can never be affected.
				if !object.LastModified.Before(cutoffTime) {
					continue
				}

				if err := s.DeleteStagingTransactionPicture(c, uid, object.PictureId, object.FileExtension); err != nil {
					failedCount++
					log.Warnf(c, "[transaction_pictures.cleanupStagingTransactionPictureObjects] failed to delete orphaned staging transaction picture \"uid:%d,id:%d\", because %s", uid, object.PictureId, err.Error())
				} else {
					removedCount++
				}
			}
		}
	}

	// Incomplete temporary upload files or other unrecognized files in the staging area are deleted after the minimum age
	for i := 0; i < len(unparsedObjects); i++ {
		object := unparsedObjects[i]

		if !object.LastModified.Before(cutoffTime) {
			continue
		}

		if err := s.deleteTransactionPictureObjectByPath(c, object.Path); err != nil {
			failedCount++
			log.Warnf(c, "[transaction_pictures.cleanupStagingTransactionPictureObjects] failed to delete unrecognized staging object \"%s\", because %s", object.Path, err.Error())
		} else {
			removedCount++
		}
	}

	return recoveredCount, removedCount, failedCount, nil
}

func (s *TransactionPictureService) cleanupOrphanedTransactionPictureObjects(c core.Context, cutoffTime time.Time) (int, int, error) {
	removedCount := 0
	failedCount := 0

	allObjects, err := s.ListTransactionPictureObjects(c, "")

	if err != nil {
		return removedCount, failedCount, err
	}

	parsedObjects, unparsedObjects := groupTransactionPictureObjects(allObjects, false)

	// Unrecognized files in the final area are never deleted automatically, they may not be managed by ezBookkeeping
	for i := 0; i < len(unparsedObjects); i++ {
		log.Warnf(c, "[transaction_pictures.cleanupOrphanedTransactionPictureObjects] skip unrecognized object \"%s\" in transaction picture object storage", unparsedObjects[i].Path)
	}

	for uid, objects := range parsedObjects {
		pictureInfoMap, err := s.getPictureInfoMapByPictureIdsAnyState(c, uid, getTransactionPictureIdsFromObjectInfos(objects))

		if err != nil {
			failedCount += len(objects)
			log.Warnf(c, "[transaction_pictures.cleanupOrphanedTransactionPictureObjects] failed to get transaction picture records for user \"uid:%d\", because %s", uid, err.Error())
			continue
		}

		for i := 0; i < len(objects); i++ {
			object := objects[i]
			pictureInfo, exists := pictureInfoMap[object.PictureId]

			if exists && !pictureInfo.Deleted {
				continue
			}

			if exists && pictureInfo.Deleted {
				if pictureInfo.DeletedUnixTime > 0 && pictureInfo.DeletedUnixTime <= cutoffTime.Unix() {
					if err := s.deleteTransactionPictureObjectsAndRecord(c, pictureInfo); err != nil {
						failedCount++
						log.Warnf(c, "[transaction_pictures.cleanupOrphanedTransactionPictureObjects] failed to delete objects of deleted transaction picture \"uid:%d,id:%d\", because %s", uid, object.PictureId, err.Error())
					} else {
						removedCount++
					}
				}

				continue
			}

			// There is no record for the object. It may be an orphan left by a failed upload in the old upload flow.
			// Only delete it after it is older than the minimum age, so an in-flight publish can never be affected.
			if !object.LastModified.Before(cutoffTime) {
				continue
			}

			if err := s.DeleteTransactionPicture(c, uid, object.PictureId, object.FileExtension); err != nil {
				failedCount++
				log.Warnf(c, "[transaction_pictures.cleanupOrphanedTransactionPictureObjects] failed to delete orphaned transaction picture \"uid:%d,id:%d\", because %s", uid, object.PictureId, err.Error())
			} else {
				removedCount++
			}
		}
	}

	return removedCount, failedCount, nil
}

func (s *TransactionPictureService) deleteTransactionPictureObjectsAndRecord(c core.Context, pictureInfo *models.TransactionPictureInfo) error {
	errFinal := s.DeleteTransactionPicture(c, pictureInfo.Uid, pictureInfo.PictureId, pictureInfo.PictureExtension)
	errStaging := s.DeleteStagingTransactionPicture(c, pictureInfo.Uid, pictureInfo.PictureId, pictureInfo.PictureExtension)

	if errFinal != nil {
		return errFinal
	}

	if errStaging != nil {
		return errStaging
	}

	return s.hardDeletePictureInfoRecord(c, pictureInfo.Uid, pictureInfo.PictureId)
}

func (s *TransactionPictureService) hardDeletePictureInfoRecord(c core.Context, uid int64, pictureId int64) error {
	return s.UserDataDB(uid).DoTransaction(c, func(sess *xorm.Session) error {
		_, err := sess.Where("uid=? AND picture_id=?", uid, pictureId).Delete(&models.TransactionPictureInfo{})
		return err
	})
}

func (s *TransactionPictureService) deleteTransactionPictureObjectByPath(c core.Context, path string) error {
	return s.storageContainer().DeleteTransactionPicture(c, path)
}

func (s *TransactionPictureService) getPictureInfoMapByPictureIdsAnyState(c core.Context, uid int64, pictureIds []int64) (map[int64]*models.TransactionPictureInfo, error) {
	pictureInfoMap := make(map[int64]*models.TransactionPictureInfo)

	if len(pictureIds) < 1 {
		return pictureInfoMap, nil
	}

	for startIndex := 0; startIndex < len(pictureIds); startIndex += transactionPictureCleanupBatchSize {
		endIndex := startIndex + transactionPictureCleanupBatchSize

		if endIndex > len(pictureIds) {
			endIndex = len(pictureIds)
		}

		batchPictureIds := pictureIds[startIndex:endIndex]
		var pictureInfos []*models.TransactionPictureInfo
		err := s.UserDataDB(uid).NewSession(c).Where("uid=?", uid).In("picture_id", batchPictureIds).Find(&pictureInfos)

		if err != nil {
			return nil, err
		}

		for i := 0; i < len(pictureInfos); i++ {
			pictureInfoMap[pictureInfos[i].PictureId] = pictureInfos[i]
		}
	}

	return pictureInfoMap, nil
}

type transactionPictureObjectInfo struct {
	storage.ObjectInStorageInfo
	Uid           int64
	PictureId     int64
	FileExtension string
}

func groupTransactionPictureObjects(objects []storage.ObjectInStorageInfo, inStaging bool) (map[int64][]transactionPictureObjectInfo, []storage.ObjectInStorageInfo) {
	parsedObjects := make(map[int64][]transactionPictureObjectInfo)
	unparsedObjects := make([]storage.ObjectInStorageInfo, 0)

	for i := 0; i < len(objects); i++ {
		object := objects[i]
		relativePath := object.Path

		if inStaging {
			if !strings.HasPrefix(filepath.ToSlash(relativePath), transactionPictureStagingPathPrefix+"/") {
				unparsedObjects = append(unparsedObjects, object)
				continue
			}

			relativePath = strings.TrimPrefix(filepath.ToSlash(relativePath), transactionPictureStagingPathPrefix+"/")
		} else if strings.HasPrefix(filepath.ToSlash(relativePath), transactionPictureStagingPathPrefix+"/") {
			continue
		}

		uid, pictureId, fileExtension, ok := parseTransactionPictureObjectPath(relativePath)

		if !ok {
			unparsedObjects = append(unparsedObjects, object)
			continue
		}

		parsedObjects[uid] = append(parsedObjects[uid], transactionPictureObjectInfo{
			ObjectInStorageInfo: object,
			Uid:                 uid,
			PictureId:           pictureId,
			FileExtension:       fileExtension,
		})
	}

	return parsedObjects, unparsedObjects
}

func parseTransactionPictureObjectPath(objectRelativePath string) (int64, int64, string, bool) {
	pathParts := strings.Split(filepath.ToSlash(objectRelativePath), "/")

	if len(pathParts) != 2 || pathParts[0] == "" || pathParts[1] == "" {
		return 0, 0, "", false
	}

	uid, err := utils.StringToInt64(pathParts[0])

	if err != nil || uid <= 0 {
		return 0, 0, "", false
	}

	fileName := pathParts[1]
	fileExtension := strings.ToLower(utils.GetFileNameExtension(fileName))

	if utils.GetImageContentType(fileExtension) == "" {
		return 0, 0, "", false
	}

	fileBaseName := utils.GetFileNameWithoutExtension(fileName)
	pictureId, err := utils.StringToInt64(fileBaseName)

	if err != nil || pictureId <= 0 {
		return 0, 0, "", false
	}

	return uid, pictureId, fileExtension, true
}

func getTransactionPictureIdsFromObjectInfos(objects []transactionPictureObjectInfo) []int64 {
	pictureIds := make([]int64, 0, len(objects))

	for i := 0; i < len(objects); i++ {
		pictureIds = append(pictureIds, objects[i].PictureId)
	}

	return pictureIds
}
